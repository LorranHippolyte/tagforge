package safety

import (
	"errors"
	"fmt"
	"strings"
)

// Mode controls which operations are permitted by the safety policy.
type Mode string

const (
	// ModeReadOnly permits only non-mutating inspection and preview operations.
	ModeReadOnly Mode = "read-only"
	// ModeWorkspaceOnly permits reads, previews, workspace mutations, and draft version creation.
	// It blocks publishing and container/account-level destructive mutations.
	ModeWorkspaceOnly Mode = "workspace-only"
	// ModeFull permits all GTM operations except user permissions unless explicitly unlocked.
	ModeFull Mode = "full"
)

// Resource identifies a Google Tag Manager API v2 resource type.
type Resource string

const (
	ResourceAccount         Resource = "account"
	ResourceContainer       Resource = "container"
	ResourceWorkspace       Resource = "workspace"
	ResourceTag             Resource = "tag"
	ResourceTrigger         Resource = "trigger"
	ResourceVariable        Resource = "variable"
	ResourceBuiltInVariable Resource = "builtin_variable"
	ResourceFolder          Resource = "folder"
	ResourceTemplate        Resource = "template"
	ResourceClient          Resource = "client"
	ResourceTransformation  Resource = "transformation"
	ResourceZone            Resource = "zone"
	ResourceEnvironment     Resource = "environment"
	ResourceVersion         Resource = "version"
	ResourceVersionHeader   Resource = "version_header"
	ResourceGtagConfig      Resource = "gtag_config"
	ResourceDestination     Resource = "destination"
	ResourceUserPermission  Resource = "user_permission"
)

// Action identifies an operation against a GTM resource.
type Action string

const (
	ActionRead          Action = "read"
	ActionCreate        Action = "create"
	ActionUpdate        Action = "update"
	ActionDelete        Action = "delete"
	ActionRevert        Action = "revert"
	ActionSync          Action = "sync"
	ActionPreview       Action = "preview"
	ActionCreateVersion Action = "create_version"
	ActionPublish       Action = "publish"
	ActionBulkUpdate    Action = "bulk_update"
	ActionEnable        Action = "enable"
	ActionDisable       Action = "disable"
)

var (
	// ErrBlocked indicates an operation was denied by the active safety policy.
	ErrBlocked = errors.New("operation blocked by safety policy")
	// ErrDuplicate indicates an entity with the same name already exists.
	ErrDuplicate = errors.New("duplicate entity name")
)

// BlockedError provides structured context and remediation guidance when an action is denied.
type BlockedError struct {
	Resource Resource
	Action   Action
	Mode     Mode
	Reason   string
	Hint     string
}

func (e *BlockedError) Error() string {
	if e.Hint != "" {
		return fmt.Sprintf("safety policy (%s): %s %s blocked — %s (%s)", e.Mode, e.Action, e.Resource, e.Reason, e.Hint)
	}
	return fmt.Sprintf("safety policy (%s): %s %s blocked — %s", e.Mode, e.Action, e.Resource, e.Reason)
}

func (e *BlockedError) Unwrap() error {
	return ErrBlocked
}

// DuplicateError reports a colliding entity name detected prior to creation.
type DuplicateError struct {
	Kind         string
	Candidate    string
	ExistingName string
}

func (e *DuplicateError) Error() string {
	return fmt.Sprintf("%s %q already exists (matches %q); rename or pass allowDuplicate to override", e.Kind, e.Candidate, e.ExistingName)
}

func (e *DuplicateError) Unwrap() error {
	return ErrDuplicate
}

// Request describes a proposed GTM operation evaluated by Policy.Check.
type Request struct {
	Resource Resource
	Action   Action
	Confirm  bool
}

// Policy enforces mode boundaries, confirmation gates, and privilege isolation.
type Policy struct {
	Mode                 Mode
	AllowUserPermissions bool
}

// DefaultPolicy returns the safest default policy for everyday workspace authoring.
func DefaultPolicy() *Policy {
	return &Policy{
		Mode:                 ModeWorkspaceOnly,
		AllowUserPermissions: false,
	}
}

// ParseMode parses and validates a safety mode string.
// An empty string resolves to ModeWorkspaceOnly.
func ParseMode(raw string) (Mode, error) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized == "" {
		return ModeWorkspaceOnly, nil
	}
	switch Mode(normalized) {
	case ModeReadOnly, ModeWorkspaceOnly, ModeFull:
		return Mode(normalized), nil
	default:
		return "", fmt.Errorf("invalid mode %q: must be one of %q, %q, %q", raw, ModeReadOnly, ModeWorkspaceOnly, ModeFull)
	}
}

var workspaceScopedResources = map[Resource]bool{
	ResourceWorkspace:       true,
	ResourceTag:             true,
	ResourceTrigger:         true,
	ResourceVariable:        true,
	ResourceBuiltInVariable: true,
	ResourceFolder:          true,
	ResourceTemplate:        true,
	ResourceClient:          true,
	ResourceTransformation:  true,
	ResourceZone:            true,
	ResourceGtagConfig:      true,
}

// Check verifies whether req is permitted under the Policy before any API call is issued.
func (p *Policy) Check(req Request) error {
	mode := p.Mode
	if mode == "" {
		mode = ModeWorkspaceOnly
	}

	if _, err := ParseMode(string(mode)); err != nil {
		return &BlockedError{
			Resource: req.Resource,
			Action:   req.Action,
			Mode:     mode,
			Reason:   err.Error(),
		}
	}

	if req.Resource == ResourceUserPermission && !p.AllowUserPermissions {
		return &BlockedError{
			Resource: req.Resource,
			Action:   req.Action,
			Mode:     mode,
			Reason:   "user permission management is disabled by default",
			Hint:     "pass --allow-user-permissions or set TAGFORGE_ALLOW_USER_PERMISSIONS=true",
		}
	}

	if req.Action == ActionRead || req.Action == ActionPreview {
		return nil
	}

	switch mode {
	case ModeReadOnly:
		return &BlockedError{
			Resource: req.Resource,
			Action:   req.Action,
			Mode:     mode,
			Reason:   "mutating actions are not allowed in read-only mode",
			Hint:     "use --mode workspace-only or --mode full",
		}
	case ModeWorkspaceOnly:
		if req.Action == ActionPublish {
			return &BlockedError{
				Resource: req.Resource,
				Action:   req.Action,
				Mode:     mode,
				Reason:   "publishing live container versions is blocked in workspace-only mode",
				Hint:     "use --mode full --confirm",
			}
		}
		allowedWorkspaceMutation := workspaceScopedResources[req.Resource] ||
			(req.Resource == ResourceVersion && req.Action == ActionCreateVersion)
		if !allowedWorkspaceMutation {
			return &BlockedError{
				Resource: req.Resource,
				Action:   req.Action,
				Mode:     mode,
				Reason:   "container/account-level mutations are blocked in workspace-only mode",
				Hint:     "use --mode full (and --confirm for destructive actions)",
			}
		}
	case ModeFull:
		// All non-user-permission mutations are permitted subject to confirmation gates.
	}

	if requiresConfirmation(req.Action) && !req.Confirm {
		return &BlockedError{
			Resource: req.Resource,
			Action:   req.Action,
			Mode:     mode,
			Reason:   fmt.Sprintf("%s requires explicit confirmation", req.Action),
			Hint:     "re-run with --confirm",
		}
	}

	return nil
}

func requiresConfirmation(action Action) bool {
	switch action {
	case ActionDelete, ActionPublish, ActionDisable:
		return true
	default:
		return false
	}
}

// CheckDuplicateName prevents accidental duplicate creations on retries using
// case-insensitive trimmed comparison.
func CheckDuplicateName(kind string, existing []string, candidate string, allowDuplicate bool) error {
	if allowDuplicate {
		return nil
	}
	normalizedCandidate := strings.ToLower(strings.TrimSpace(candidate))
	if normalizedCandidate == "" {
		return nil
	}
	for _, name := range existing {
		if strings.ToLower(strings.TrimSpace(name)) == normalizedCandidate {
			return &DuplicateError{
				Kind:         kind,
				Candidate:    candidate,
				ExistingName: name,
			}
		}
	}
	return nil
}
