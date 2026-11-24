package trackingplan

import (
	"fmt"
	"strings"
)

// SnippetTarget selects the language/framework of generated dataLayer snippets.
type SnippetTarget string

const (
	TargetTS      SnippetTarget = "ts"
	TargetJS      SnippetTarget = "js"
	TargetNextJS  SnippetTarget = "nextjs"
	TargetShopify SnippetTarget = "shopify"
)

// ParseSnippetTarget validates a snippet target string (empty defaults to ts).
func ParseSnippetTarget(raw string) (SnippetTarget, error) {
	switch SnippetTarget(strings.ToLower(strings.TrimSpace(raw))) {
	case "", TargetTS:
		return TargetTS, nil
	case TargetJS:
		return TargetJS, nil
	case TargetNextJS:
		return TargetNextJS, nil
	case TargetShopify:
		return TargetShopify, nil
	default:
		return "", fmt.Errorf("invalid snippet target %q: must be ts, js, nextjs, or shopify", raw)
	}
}

// Input configures tracking plan generation.
type Input struct {
	AccountID     string        `json:"accountId"`
	ContainerID   string        `json:"containerId"`
	WorkspaceID   string        `json:"workspaceId,omitempty"`
	Live          bool          `json:"live,omitempty"`
	SnippetTarget SnippetTarget `json:"snippetTarget,omitempty"`
}

// DestinationEntry describes a tag firing in response to an event/trigger.
type DestinationEntry struct {
	Platform      string   `json:"platform"`
	TagID         string   `json:"tagId"`
	TagName       string   `json:"tagName"`
	TagType       string   `json:"tagType"`
	Paused        bool     `json:"paused,omitempty"`
	ConsentStatus string   `json:"consentStatus,omitempty"`
	ConsentTypes  []string `json:"consentTypes,omitempty"`
	Parameters    []string `json:"parameters,omitempty"`
}

// EventEntry maps a logical tracking event / trigger to its firing destinations and dataLayer keys.
type EventEntry struct {
	EventName     string             `json:"eventName"`
	TriggerID     string             `json:"triggerId"`
	TriggerName   string             `json:"triggerName"`
	TriggerType   string             `json:"triggerType"`
	Condition     string             `json:"condition,omitempty"`
	Destinations  []DestinationEntry `json:"destinations"`
	DataLayerKeys []string           `json:"dataLayerKeys,omitempty"`
}

// DataLayerVar catalogs a Data Layer Variable ('v') found in the container.
type DataLayerVar struct {
	VariableID   string   `json:"variableId"`
	VariableName string   `json:"variableName"`
	KeyPath      string   `json:"keyPath"`
	DefaultValue string   `json:"defaultValue,omitempty"`
	UsedByTags   []string `json:"usedByTags,omitempty"`
}

// ConstantRef records a Constant ('c') variable with sensitive values redacted.
type ConstantRef struct {
	VariableName string `json:"variableName"`
	Value        string `json:"value"`
}

// Plan represents a living GTM tracking plan extracted from container configuration.
type Plan struct {
	AccountID     string         `json:"accountId"`
	ContainerID   string         `json:"containerId"`
	WorkspaceID   string         `json:"workspaceId,omitempty"`
	LiveVersionID string         `json:"liveVersionId,omitempty"`
	Target        string         `json:"target"`
	SnippetTarget SnippetTarget  `json:"snippetTarget"`
	Events        []EventEntry   `json:"events"`
	DataLayerVars []DataLayerVar `json:"dataLayerVars,omitempty"`
	Constants     []ConstantRef  `json:"constants,omitempty"`
	Snippets      string         `json:"snippets"`
}
