package blueprints

// Spec represents a declarative, vendor-accurate GTM blueprint loaded from YAML.
type Spec struct {
	ID               string                `yaml:"id" json:"id"`
	Name             string                `yaml:"name" json:"name"`
	Version          string                `yaml:"version" json:"version"`
	Context          string                `yaml:"context" json:"context"`
	Category         string                `yaml:"category" json:"category"`
	Description      string                `yaml:"description" json:"description"`
	DocsURLs         []string              `yaml:"docs_urls,omitempty" json:"docs_urls,omitempty"`
	Params           []ParamSpec           `yaml:"params,omitempty" json:"params,omitempty"`
	BuiltInVariables []string              `yaml:"built_in_variables,omitempty" json:"built_in_variables,omitempty"`
	GalleryTemplates []GalleryTemplateSpec `yaml:"gallery_templates,omitempty" json:"gallery_templates,omitempty"`
	Folders          []FolderSpec          `yaml:"folders,omitempty" json:"folders,omitempty"`
	Variables        []VariableSpec        `yaml:"variables,omitempty" json:"variables,omitempty"`
	Triggers         []TriggerSpec         `yaml:"triggers,omitempty" json:"triggers,omitempty"`
	Tags             []TagSpec             `yaml:"tags,omitempty" json:"tags,omitempty"`
	DataLayerSnippet []DataLayerSnippet    `yaml:"data_layer_snippets,omitempty" json:"data_layer_snippets,omitempty"`
}

// ParamSpec defines an input parameter accepted by a blueprint.
type ParamSpec struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description" json:"description"`
	Required    bool   `yaml:"required,omitempty" json:"required,omitempty"`
	Default     string `yaml:"default,omitempty" json:"default,omitempty"`
	Pattern     string `yaml:"pattern,omitempty" json:"pattern,omitempty"`
	Secret      bool   `yaml:"secret,omitempty" json:"secret,omitempty"`
}

// GalleryTemplateSpec describes a Community Template Gallery template required by a blueprint.
type GalleryTemplateSpec struct {
	Alias                  string `yaml:"alias" json:"alias"`
	Owner                  string `yaml:"owner" json:"owner"`
	Repository             string `yaml:"repository" json:"repository"`
	SHA                    string `yaml:"sha,omitempty" json:"sha,omitempty"`
	AcknowledgePermissions bool   `yaml:"acknowledge_permissions,omitempty" json:"acknowledge_permissions,omitempty"`
}

// FolderSpec describes a GTM folder created by the blueprint.
type FolderSpec struct {
	Key   string `yaml:"key" json:"key"`
	Name  string `yaml:"name" json:"name"`
	Notes string `yaml:"notes,omitempty" json:"notes,omitempty"`
}

// VariableSpec describes a GTM user-defined variable created by the blueprint.
type VariableSpec struct {
	Key               string          `yaml:"key,omitempty" json:"key,omitempty"`
	Name              string          `yaml:"name" json:"name"`
	Type              string          `yaml:"type" json:"type"`
	ParentFolder      string          `yaml:"parent_folder,omitempty" json:"parent_folder,omitempty"`
	Notes             string          `yaml:"notes,omitempty" json:"notes,omitempty"`
	WhenParamNonEmpty string          `yaml:"when_param_non_empty,omitempty" json:"when_param_non_empty,omitempty"`
	Parameters        []ParameterSpec `yaml:"parameters,omitempty" json:"parameters,omitempty"`
}

// TriggerSpec describes a GTM trigger created by the blueprint.
type TriggerSpec struct {
	Key                string          `yaml:"key" json:"key"`
	Name               string          `yaml:"name" json:"name"`
	Type               string          `yaml:"type" json:"type"`
	ParentFolder       string          `yaml:"parent_folder,omitempty" json:"parent_folder,omitempty"`
	Notes              string          `yaml:"notes,omitempty" json:"notes,omitempty"`
	WhenParamNonEmpty  string          `yaml:"when_param_non_empty,omitempty" json:"when_param_non_empty,omitempty"`
	Filters            []ConditionSpec `yaml:"filters,omitempty" json:"filters,omitempty"`
	CustomEventFilters []ConditionSpec `yaml:"custom_event_filters,omitempty" json:"custom_event_filters,omitempty"`
	AutoEventFilters   []ConditionSpec `yaml:"auto_event_filters,omitempty" json:"auto_event_filters,omitempty"`
	Parameters         []ParameterSpec `yaml:"parameters,omitempty" json:"parameters,omitempty"`
}

// TagSpec describes a GTM tag created by the blueprint.
type TagSpec struct {
	Key               string          `yaml:"key,omitempty" json:"key,omitempty"`
	Name              string          `yaml:"name" json:"name"`
	Type              string          `yaml:"type" json:"type"`
	ParentFolder      string          `yaml:"parent_folder,omitempty" json:"parent_folder,omitempty"`
	FiringTriggers    []string        `yaml:"firing_triggers,omitempty" json:"firing_triggers,omitempty"`
	BlockingTriggers  []string        `yaml:"blocking_triggers,omitempty" json:"blocking_triggers,omitempty"`
	TagFiringOption   string          `yaml:"tag_firing_option,omitempty" json:"tag_firing_option,omitempty"`
	Notes             string          `yaml:"notes,omitempty" json:"notes,omitempty"`
	WhenParamNonEmpty string          `yaml:"when_param_non_empty,omitempty" json:"when_param_non_empty,omitempty"`
	Parameters        []ParameterSpec `yaml:"parameters,omitempty" json:"parameters,omitempty"`
	Consent           *ConsentSpec    `yaml:"consent,omitempty" json:"consent,omitempty"`
}

// ParameterSpec mirrors a GTM API v2 Parameter recursively (TEMPLATE, BOOLEAN, LIST, MAP, INTEGER).
type ParameterSpec struct {
	Key               string          `yaml:"key,omitempty" json:"key,omitempty"`
	Type              string          `yaml:"type" json:"type"`
	Value             string          `yaml:"value,omitempty" json:"value,omitempty"`
	WhenParamNonEmpty string          `yaml:"when_param_non_empty,omitempty" json:"when_param_non_empty,omitempty"`
	List              []ParameterSpec `yaml:"list,omitempty" json:"list,omitempty"`
	Map               []ParameterSpec `yaml:"map,omitempty" json:"map,omitempty"`
}

// ConditionSpec mirrors a GTM API v2 trigger condition.
type ConditionSpec struct {
	Type       string          `yaml:"type" json:"type"`
	Parameters []ParameterSpec `yaml:"parameters" json:"parameters"`
}

// ConsentSpec mirrors GTM TagConsentSetting.
type ConsentSpec struct {
	Status string   `yaml:"status" json:"status"`
	Types  []string `yaml:"types,omitempty" json:"types,omitempty"`
}

// DataLayerSnippet documents the application-side dataLayer contract matching the blueprint.
type DataLayerSnippet struct {
	Title       string `yaml:"title" json:"title"`
	Language    string `yaml:"language" json:"language"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	Code        string `yaml:"code" json:"code"`
}
