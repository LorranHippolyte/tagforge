package auditor

import (
	"fmt"
	"strings"

	"google.golang.org/api/tagmanager/v2"
)

// Severity expresses finding impact in descending order.
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"
)

// ParseSeverity validates and normalizes a severity string (empty defaults to low).
func ParseSeverity(raw string) (Severity, error) {
	switch Severity(strings.ToLower(strings.TrimSpace(raw))) {
	case "", SeverityLow:
		return SeverityLow, nil
	case SeverityMedium:
		return SeverityMedium, nil
	case SeverityHigh:
		return SeverityHigh, nil
	case SeverityCritical:
		return SeverityCritical, nil
	default:
		return "", fmt.Errorf("invalid severity %q: must be critical, high, medium, or low", raw)
	}
}

// SeverityRank returns an ordinal weight for sorting and threshold comparisons.
func SeverityRank(s Severity) int {
	switch s {
	case SeverityCritical:
		return 4
	case SeverityHigh:
		return 3
	case SeverityMedium:
		return 2
	case SeverityLow:
		return 1
	default:
		return 0
	}
}

// Category groups findings by audit domain.
type Category string

const (
	CategoryPrivacy     Category = "privacy"
	CategorySecurity    Category = "security"
	CategoryReliability Category = "reliability"
	CategoryHygiene     Category = "hygiene"
)

// Finding describes a single deterministic rule violation discovered in a GTM container.
type Finding struct {
	RuleID      string   `json:"ruleId"`
	Severity    Severity `json:"severity"`
	Category    Category `json:"category"`
	EntityKind  string   `json:"entityKind"`
	EntityID    string   `json:"entityId,omitempty"`
	EntityName  string   `json:"entityName"`
	Title       string   `json:"title"`
	Message     string   `json:"message"`
	Remediation string   `json:"remediation"`
}

// Input controls which workspace or live version is audited and what findings are returned.
type Input struct {
	AccountID   string   `json:"accountId"`
	ContainerID string   `json:"containerId"`
	WorkspaceID string   `json:"workspaceId,omitempty"`
	Live        bool     `json:"live,omitempty"`
	MinSeverity Severity `json:"minSeverity,omitempty"`
}

// Snapshot holds all GTM entities collected from either a workspace or live version.
type Snapshot struct {
	AccountID        string
	ContainerID      string
	WorkspaceID      string
	LiveVersionID    string
	TargetLabel      string
	Tags             []*tagmanager.Tag
	Triggers         []*tagmanager.Trigger
	Variables        []*tagmanager.Variable
	BuiltInVariables []*tagmanager.BuiltInVariable
	Folders          []*tagmanager.Folder
	Templates        []*tagmanager.CustomTemplate
	Clients          []*tagmanager.Client
	Transformations  []*tagmanager.Transformation
	Workspaces       []*tagmanager.Workspace
	VersionHeaders   []*tagmanager.ContainerVersionHeader
}

// Summary aggregates overall score, grade, and entity/severity counts.
type Summary struct {
	Score         int              `json:"score"`
	Grade         string           `json:"grade"`
	TotalFindings int              `json:"totalFindings"`
	BySeverity    map[Severity]int `json:"bySeverity"`
	Counts        map[string]int   `json:"counts"`
}

// Report is the complete output of a container audit.
type Report struct {
	AccountID     string    `json:"accountId"`
	ContainerID   string    `json:"containerId"`
	WorkspaceID   string    `json:"workspaceId,omitempty"`
	LiveVersionID string    `json:"liveVersionId,omitempty"`
	Target        string    `json:"target"`
	Summary       Summary   `json:"summary"`
	Findings      []Finding `json:"findings"`
}
