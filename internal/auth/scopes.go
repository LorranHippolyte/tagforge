package auth

import (
	"slices"

	"github.com/LorranHippolyte/tagforge/internal/safety"
)

const (
	ScopeReadOnly              = "https://www.googleapis.com/auth/tagmanager.readonly"
	ScopeEditContainers        = "https://www.googleapis.com/auth/tagmanager.edit.containers"
	ScopeEditContainerVersions = "https://www.googleapis.com/auth/tagmanager.edit.containerversions"
	ScopePublish               = "https://www.googleapis.com/auth/tagmanager.publish"
	ScopeDeleteContainers      = "https://www.googleapis.com/auth/tagmanager.delete.containers"
	ScopeManageUsers           = "https://www.googleapis.com/auth/tagmanager.manage.users"
)

// ScopesForMode returns the least-privilege Google Tag Manager OAuth scopes
// required for the requested safety mode.
func ScopesForMode(mode safety.Mode, allowUserPermissions bool) []string {
	if mode == "" {
		mode = safety.ModeWorkspaceOnly
	}

	var scopes []string
	switch mode {
	case safety.ModeReadOnly:
		scopes = []string{ScopeReadOnly}
	case safety.ModeWorkspaceOnly:
		scopes = []string{
			ScopeReadOnly,
			ScopeEditContainers,
			ScopeEditContainerVersions,
		}
	case safety.ModeFull:
		scopes = []string{
			ScopeReadOnly,
			ScopeEditContainers,
			ScopeEditContainerVersions,
			ScopePublish,
			ScopeDeleteContainers,
		}
	default:
		scopes = []string{ScopeReadOnly}
	}

	if allowUserPermissions && !slices.Contains(scopes, ScopeManageUsers) {
		scopes = append(scopes, ScopeManageUsers)
	}

	return scopes
}

// MissingScopes returns scopes required by mode that are absent from granted.
func MissingScopes(granted []string, mode safety.Mode, allowUserPermissions bool) []string {
	required := ScopesForMode(mode, allowUserPermissions)
	var missing []string
	for _, req := range required {
		if !slices.Contains(granted, req) {
			missing = append(missing, req)
		}
	}
	return missing
}
