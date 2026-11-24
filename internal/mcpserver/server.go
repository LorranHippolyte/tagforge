package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/LorranHippolyte/tagforge/internal/auditor"
	"github.com/LorranHippolyte/tagforge/internal/auth"
	"github.com/LorranHippolyte/tagforge/internal/blueprints"
	"github.com/LorranHippolyte/tagforge/internal/gtm"
	"github.com/LorranHippolyte/tagforge/internal/safety"
	"github.com/LorranHippolyte/tagforge/internal/trackingplan"
	"github.com/LorranHippolyte/tagforge/internal/version"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/api/tagmanager/v2"
)

// Config configures the local MCP server wiring existing tagforge engines.
type Config struct {
	Client       *gtm.Client
	Policy       *safety.Policy
	Registry     *blueprints.Registry
	AuthStatusFn func(context.Context) (*auth.StatusReport, error)
	Version      string
}

// NewServer constructs an MCP Server exposing all 17 tagforge tools.
func NewServer(cfg Config) (*mcp.Server, error) {
	if cfg.Client == nil {
		return nil, errors.New("gtm client is required")
	}
	reg := cfg.Registry
	var err error
	if reg == nil {
		reg, err = blueprints.NewDefaultRegistry()
		if err != nil {
			return nil, fmt.Errorf("load blueprints registry: %w", err)
		}
	}
	ver := strings.TrimSpace(cfg.Version)
	if ver == "" {
		ver = version.Current().Version
	}

	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "tagforge",
		Title:   "tagforge — Safe GTM Automation & Blueprints",
		Version: ver,
	}, nil)

	registerAuthAndGuideTools(srv, cfg)
	registerAccountContainerWorkspaceTools(srv, cfg.Client)
	registerEntityTools(srv, cfg.Client)
	registerVersionTools(srv, cfg.Client)
	registerBlueprintTools(srv, cfg.Client, reg)
	registerAuditAndPlanTools(srv, cfg.Client)
	registerResources(srv, cfg.Client, reg)
	registerPrompts(srv)

	return srv, nil
}

// RunStdio serves the tagforge MCP server over stdin/stdout until ctx cancels or EOF.
func RunStdio(ctx context.Context, cfg Config) error {
	srv, err := NewServer(cfg)
	if err != nil {
		return err
	}
	return srv.Run(ctx, &mcp.StdioTransport{})
}

func textJSONResult(v any) (*mcp.CallToolResult, any, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(b)},
		},
	}, nil, nil
}

func decodePayload[T any](raw map[string]any) (*T, error) {
	if len(raw) == 0 {
		return nil, errors.New("config payload object is required for create/update")
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("encode config payload: %w", err)
	}
	var out T
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("parse config payload: %w", err)
	}
	return &out, nil
}

func registerAuthAndGuideTools(srv *mcp.Server, cfg Config) {
	type authStatusIn struct{}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "gtm_auth_status",
		Description: "Inspect active Google Tag Manager authentication source, token expiry, and OAuth scopes against the active safety mode without revealing tokens.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ authStatusIn) (*mcp.CallToolResult, any, error) {
		if cfg.AuthStatusFn == nil {
			return textJSONResult(map[string]any{
				"authenticated": true,
				"mode":          cfg.Client.Policy().Mode,
				"note":          "custom client session",
			})
		}
		rep, err := cfg.AuthStatusFn(ctx)
		if err != nil {
			return nil, nil, err
		}
		return textJSONResult(rep)
	})

	type guideIn struct {
		Topic string `json:"topic" jsonschema:"Best-practice topic: safe_editing, naming, consent_mode_v2, ga4_ecommerce, google_ads_enhanced, meta_capi_dedup, or server_side"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "gtm_guide",
		Description: "Return vendor-accurate official GTM guidance on safe editing, naming, Consent Mode v2, GA4 ecommerce, Google Ads Enhanced Conversions, Meta Pixel/CAPI deduplication, or sGTM. Makes no API calls.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in guideIn) (*mcp.CallToolResult, any, error) {
		text, ok := Guides[strings.TrimSpace(in.Topic)]
		if !ok {
			return nil, nil, fmt.Errorf("unknown guide topic %q; valid topics: %s", in.Topic, strings.Join(GuideTopics(), ", "))
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, nil, nil
	})
}

func registerAccountContainerWorkspaceTools(srv *mcp.Server, client *gtm.Client) {
	type accountIn struct {
		Action    string `json:"action" jsonschema:"Operation: list or get"`
		AccountID string `json:"accountId,omitempty" jsonschema:"GTM Account ID (required for get)"`
		Fresh     bool   `json:"fresh,omitempty" jsonschema:"Bypass list snapshot cache"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "gtm_account",
		Description: "Read Google Tag Manager accounts (list or get).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in accountIn) (*mcp.CallToolResult, any, error) {
		switch in.Action {
		case "list":
			res, err := client.ListAccounts(ctx, gtm.ListOptions{Fresh: in.Fresh})
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		case "get":
			res, err := client.GetAccount(ctx, in.AccountID)
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		default:
			return nil, nil, fmt.Errorf("unsupported action %q for gtm_account (use list or get)", in.Action)
		}
	})

	type containerIn struct {
		Action      string `json:"action" jsonschema:"Operation: list, get, lookup, or snippet"`
		AccountID   string `json:"accountId,omitempty" jsonschema:"GTM Account ID (required for list, get, snippet)"`
		ContainerID string `json:"containerId,omitempty" jsonschema:"GTM Container ID (required for get, snippet)"`
		PublicID    string `json:"publicId,omitempty" jsonschema:"GTM public ID like GTM-XXXXXXX (required for lookup)"`
		Fresh       bool   `json:"fresh,omitempty" jsonschema:"Bypass list snapshot cache"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "gtm_container",
		Description: "Inspect Google Tag Manager containers (list, get, lookup by GTM-XXXXXXX, or get installation snippet).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in containerIn) (*mcp.CallToolResult, any, error) {
		switch in.Action {
		case "list":
			res, err := client.ListContainers(ctx, in.AccountID, gtm.ListOptions{Fresh: in.Fresh})
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		case "get":
			res, err := client.GetContainer(ctx, in.AccountID, in.ContainerID)
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		case "lookup":
			res, err := client.LookupContainer(ctx, in.PublicID)
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		case "snippet":
			res, err := client.GetContainerSnippet(ctx, in.AccountID, in.ContainerID)
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		default:
			return nil, nil, fmt.Errorf("unsupported action %q for gtm_container", in.Action)
		}
	})

	type workspaceIn struct {
		Action         string         `json:"action" jsonschema:"Operation: list, get, create, status, sync, preview, create_version, or bulk_update"`
		AccountID      string         `json:"accountId" jsonschema:"GTM Account ID"`
		ContainerID    string         `json:"containerId" jsonschema:"GTM Container ID"`
		WorkspaceID    string         `json:"workspaceId,omitempty" jsonschema:"GTM Workspace ID (required except for list and create)"`
		Name           string         `json:"name,omitempty" jsonschema:"Name for workspace create or container version"`
		Description    string         `json:"description,omitempty" jsonschema:"Description for workspace create"`
		Notes          string         `json:"notes,omitempty" jsonschema:"Release notes for create_version"`
		AllowDuplicate bool           `json:"allowDuplicate,omitempty" jsonschema:"Allow duplicate workspace name on create"`
		Fresh          bool           `json:"fresh,omitempty" jsonschema:"Bypass list snapshot cache"`
		Changes        map[string]any `json:"changes,omitempty" jsonschema:"ProposedChange JSON object for bulk_update (must include changes array)"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "gtm_workspace",
		Description: "Manage GTM workspaces: list, get, create dedicated workspace, inspect status/conflicts, sync, quick preview, create_version (pre-checked), and atomic bulk_update.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in workspaceIn) (*mcp.CallToolResult, any, error) {
		switch in.Action {
		case "list":
			res, err := client.ListWorkspaces(ctx, in.AccountID, in.ContainerID, gtm.ListOptions{Fresh: in.Fresh})
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		case "get":
			res, err := client.GetWorkspace(ctx, in.AccountID, in.ContainerID, in.WorkspaceID)
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		case "create":
			res, err := client.CreateWorkspace(ctx, in.AccountID, in.ContainerID, &tagmanager.Workspace{
				Name:        in.Name,
				Description: in.Description,
			}, gtm.CreateOptions{AllowDuplicate: in.AllowDuplicate})
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		case "status":
			res, err := client.GetWorkspaceStatus(ctx, in.AccountID, in.ContainerID, in.WorkspaceID)
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		case "sync":
			res, err := client.SyncWorkspace(ctx, in.AccountID, in.ContainerID, in.WorkspaceID)
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		case "preview":
			res, err := client.QuickPreviewWorkspace(ctx, in.AccountID, in.ContainerID, in.WorkspaceID)
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		case "create_version":
			res, err := client.CreateVersion(ctx, in.AccountID, in.ContainerID, in.WorkspaceID, gtm.CreateVersionOptions{
				Name:  in.Name,
				Notes: in.Notes,
			})
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		case "bulk_update":
			payload, err := decodePayload[tagmanager.ProposedChange](in.Changes)
			if err != nil {
				return nil, nil, err
			}
			res, err := client.BulkUpdateWorkspace(ctx, in.AccountID, in.ContainerID, in.WorkspaceID, payload)
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		default:
			return nil, nil, fmt.Errorf("unsupported action %q for gtm_workspace", in.Action)
		}
	})
}

func registerEntityTools(srv *mcp.Server, client *gtm.Client) {
	type tagIn struct {
		Action         string         `json:"action" jsonschema:"Operation: list, get, create, update, delete, or revert"`
		AccountID      string         `json:"accountId" jsonschema:"GTM Account ID"`
		ContainerID    string         `json:"containerId" jsonschema:"GTM Container ID"`
		WorkspaceID    string         `json:"workspaceId" jsonschema:"GTM Workspace ID"`
		TagID          string         `json:"tagId,omitempty" jsonschema:"GTM Tag ID (required for get, update, delete, revert)"`
		Fingerprint    string         `json:"fingerprint,omitempty" jsonschema:"Tag fingerprint from get (required for update and revert)"`
		Confirm        bool           `json:"confirm,omitempty" jsonschema:"Must be true for delete"`
		AllowDuplicate bool           `json:"allowDuplicate,omitempty" jsonschema:"Allow duplicate tag name on create"`
		Fresh          bool           `json:"fresh,omitempty" jsonschema:"Bypass list snapshot cache"`
		Config         map[string]any `json:"config,omitempty" jsonschema:"Complete GTM Tag JSON object for create or update (update is a full replace)"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "gtm_tag",
		Description: "Manage GTM tags in a workspace (list, get, create, update, delete, revert). Delete requires confirm=true; update/revert require fingerprint.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in tagIn) (*mcp.CallToolResult, any, error) {
		switch in.Action {
		case "list":
			res, err := client.ListTags(ctx, in.AccountID, in.ContainerID, in.WorkspaceID, gtm.ListOptions{Fresh: in.Fresh})
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		case "get":
			res, err := client.GetTag(ctx, in.AccountID, in.ContainerID, in.WorkspaceID, in.TagID)
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		case "create":
			payload, err := decodePayload[tagmanager.Tag](in.Config)
			if err != nil {
				return nil, nil, err
			}
			res, err := client.CreateTag(ctx, in.AccountID, in.ContainerID, in.WorkspaceID, payload, gtm.CreateOptions{AllowDuplicate: in.AllowDuplicate})
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		case "update":
			payload, err := decodePayload[tagmanager.Tag](in.Config)
			if err != nil {
				return nil, nil, err
			}
			res, err := client.UpdateTag(ctx, in.AccountID, in.ContainerID, in.WorkspaceID, in.TagID, in.Fingerprint, payload)
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		case "delete":
			if err := client.DeleteTag(ctx, in.AccountID, in.ContainerID, in.WorkspaceID, in.TagID, in.Confirm); err != nil {
				return nil, nil, err
			}
			return textJSONResult(map[string]any{"deleted": true, "tagId": in.TagID})
		case "revert":
			res, err := client.RevertTag(ctx, in.AccountID, in.ContainerID, in.WorkspaceID, in.TagID, in.Fingerprint)
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		default:
			return nil, nil, fmt.Errorf("unsupported action %q for gtm_tag", in.Action)
		}
	})

	type triggerIn struct {
		Action         string         `json:"action" jsonschema:"Operation: list, get, create, update, delete, or revert"`
		AccountID      string         `json:"accountId" jsonschema:"GTM Account ID"`
		ContainerID    string         `json:"containerId" jsonschema:"GTM Container ID"`
		WorkspaceID    string         `json:"workspaceId" jsonschema:"GTM Workspace ID"`
		TriggerID      string         `json:"triggerId,omitempty" jsonschema:"GTM Trigger ID (required for get, update, delete, revert)"`
		Fingerprint    string         `json:"fingerprint,omitempty" jsonschema:"Trigger fingerprint from get (required for update and revert)"`
		Confirm        bool           `json:"confirm,omitempty" jsonschema:"Must be true for delete"`
		AllowDuplicate bool           `json:"allowDuplicate,omitempty" jsonschema:"Allow duplicate trigger name on create"`
		Fresh          bool           `json:"fresh,omitempty" jsonschema:"Bypass list snapshot cache"`
		Config         map[string]any `json:"config,omitempty" jsonschema:"Complete GTM Trigger JSON object for create or update"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "gtm_trigger",
		Description: "Manage GTM triggers in a workspace (list, get, create, update, delete, revert).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in triggerIn) (*mcp.CallToolResult, any, error) {
		switch in.Action {
		case "list":
			res, err := client.ListTriggers(ctx, in.AccountID, in.ContainerID, in.WorkspaceID, gtm.ListOptions{Fresh: in.Fresh})
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		case "get":
			res, err := client.GetTrigger(ctx, in.AccountID, in.ContainerID, in.WorkspaceID, in.TriggerID)
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		case "create":
			payload, err := decodePayload[tagmanager.Trigger](in.Config)
			if err != nil {
				return nil, nil, err
			}
			res, err := client.CreateTrigger(ctx, in.AccountID, in.ContainerID, in.WorkspaceID, payload, gtm.CreateOptions{AllowDuplicate: in.AllowDuplicate})
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		case "update":
			payload, err := decodePayload[tagmanager.Trigger](in.Config)
			if err != nil {
				return nil, nil, err
			}
			res, err := client.UpdateTrigger(ctx, in.AccountID, in.ContainerID, in.WorkspaceID, in.TriggerID, in.Fingerprint, payload)
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		case "delete":
			if err := client.DeleteTrigger(ctx, in.AccountID, in.ContainerID, in.WorkspaceID, in.TriggerID, in.Confirm); err != nil {
				return nil, nil, err
			}
			return textJSONResult(map[string]any{"deleted": true, "triggerId": in.TriggerID})
		case "revert":
			res, err := client.RevertTrigger(ctx, in.AccountID, in.ContainerID, in.WorkspaceID, in.TriggerID, in.Fingerprint)
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		default:
			return nil, nil, fmt.Errorf("unsupported action %q for gtm_trigger", in.Action)
		}
	})

	type variableIn struct {
		Action         string         `json:"action" jsonschema:"Operation: list, get, create, update, delete, or revert"`
		AccountID      string         `json:"accountId" jsonschema:"GTM Account ID"`
		ContainerID    string         `json:"containerId" jsonschema:"GTM Container ID"`
		WorkspaceID    string         `json:"workspaceId" jsonschema:"GTM Workspace ID"`
		VariableID     string         `json:"variableId,omitempty" jsonschema:"GTM Variable ID (required for get, update, delete, revert)"`
		Fingerprint    string         `json:"fingerprint,omitempty" jsonschema:"Variable fingerprint from get (required for update and revert)"`
		Confirm        bool           `json:"confirm,omitempty" jsonschema:"Must be true for delete"`
		AllowDuplicate bool           `json:"allowDuplicate,omitempty" jsonschema:"Allow duplicate variable name on create"`
		Fresh          bool           `json:"fresh,omitempty" jsonschema:"Bypass list snapshot cache"`
		Config         map[string]any `json:"config,omitempty" jsonschema:"Complete GTM Variable JSON object for create or update"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "gtm_variable",
		Description: "Manage GTM user-defined variables in a workspace (list, get, create, update, delete, revert).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in variableIn) (*mcp.CallToolResult, any, error) {
		switch in.Action {
		case "list":
			res, err := client.ListVariables(ctx, in.AccountID, in.ContainerID, in.WorkspaceID, gtm.ListOptions{Fresh: in.Fresh})
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		case "get":
			res, err := client.GetVariable(ctx, in.AccountID, in.ContainerID, in.WorkspaceID, in.VariableID)
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		case "create":
			payload, err := decodePayload[tagmanager.Variable](in.Config)
			if err != nil {
				return nil, nil, err
			}
			res, err := client.CreateVariable(ctx, in.AccountID, in.ContainerID, in.WorkspaceID, payload, gtm.CreateOptions{AllowDuplicate: in.AllowDuplicate})
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		case "update":
			payload, err := decodePayload[tagmanager.Variable](in.Config)
			if err != nil {
				return nil, nil, err
			}
			res, err := client.UpdateVariable(ctx, in.AccountID, in.ContainerID, in.WorkspaceID, in.VariableID, in.Fingerprint, payload)
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		case "delete":
			if err := client.DeleteVariable(ctx, in.AccountID, in.ContainerID, in.WorkspaceID, in.VariableID, in.Confirm); err != nil {
				return nil, nil, err
			}
			return textJSONResult(map[string]any{"deleted": true, "variableId": in.VariableID})
		case "revert":
			res, err := client.RevertVariable(ctx, in.AccountID, in.ContainerID, in.WorkspaceID, in.VariableID, in.Fingerprint)
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		default:
			return nil, nil, fmt.Errorf("unsupported action %q for gtm_variable", in.Action)
		}
	})

	type builtInIn struct {
		Action      string   `json:"action" jsonschema:"Operation: list, enable, or disable"`
		AccountID   string   `json:"accountId" jsonschema:"GTM Account ID"`
		ContainerID string   `json:"containerId" jsonschema:"GTM Container ID"`
		WorkspaceID string   `json:"workspaceId" jsonschema:"GTM Workspace ID"`
		Types       []string `json:"types,omitempty" jsonschema:"Built-in variable types (e.g. pageUrl, clickUrl, event)"`
		Confirm     bool     `json:"confirm,omitempty" jsonschema:"Must be true for disable"`
		Fresh       bool     `json:"fresh,omitempty" jsonschema:"Bypass list snapshot cache"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "gtm_builtin_variable",
		Description: "List, enable, or disable (requires confirm=true) GTM built-in variables.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in builtInIn) (*mcp.CallToolResult, any, error) {
		switch in.Action {
		case "list":
			res, err := client.ListBuiltInVariables(ctx, in.AccountID, in.ContainerID, in.WorkspaceID, gtm.ListOptions{Fresh: in.Fresh})
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		case "enable":
			res, err := client.EnableBuiltInVariables(ctx, in.AccountID, in.ContainerID, in.WorkspaceID, in.Types)
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		case "disable":
			if err := client.DisableBuiltInVariables(ctx, in.AccountID, in.ContainerID, in.WorkspaceID, in.Types, in.Confirm); err != nil {
				return nil, nil, err
			}
			return textJSONResult(map[string]any{"disabled": in.Types})
		default:
			return nil, nil, fmt.Errorf("unsupported action %q for gtm_builtin_variable", in.Action)
		}
	})

	type folderIn struct {
		Action         string `json:"action" jsonschema:"Operation: list, get, create, update, delete, or entities"`
		AccountID      string `json:"accountId" jsonschema:"GTM Account ID"`
		ContainerID    string `json:"containerId" jsonschema:"GTM Container ID"`
		WorkspaceID    string `json:"workspaceId" jsonschema:"GTM Workspace ID"`
		FolderID       string `json:"folderId,omitempty" jsonschema:"GTM Folder ID"`
		Name           string `json:"name,omitempty" jsonschema:"Folder name for create or update"`
		Notes          string `json:"notes,omitempty" jsonschema:"Folder notes"`
		Fingerprint    string `json:"fingerprint,omitempty" jsonschema:"Folder fingerprint from get (required for update)"`
		Confirm        bool   `json:"confirm,omitempty" jsonschema:"Must be true for delete"`
		AllowDuplicate bool   `json:"allowDuplicate,omitempty" jsonschema:"Allow duplicate folder name on create"`
		Fresh          bool   `json:"fresh,omitempty" jsonschema:"Bypass list snapshot cache"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "gtm_folder",
		Description: "Manage GTM folders and inspect folder entities (list, get, create, update, delete, entities).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in folderIn) (*mcp.CallToolResult, any, error) {
		switch in.Action {
		case "list":
			res, err := client.ListFolders(ctx, in.AccountID, in.ContainerID, in.WorkspaceID, gtm.ListOptions{Fresh: in.Fresh})
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		case "get":
			res, err := client.GetFolder(ctx, in.AccountID, in.ContainerID, in.WorkspaceID, in.FolderID)
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		case "create":
			res, err := client.CreateFolder(ctx, in.AccountID, in.ContainerID, in.WorkspaceID, &tagmanager.Folder{
				Name:  in.Name,
				Notes: in.Notes,
			}, gtm.CreateOptions{AllowDuplicate: in.AllowDuplicate})
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		case "update":
			res, err := client.UpdateFolder(ctx, in.AccountID, in.ContainerID, in.WorkspaceID, in.FolderID, in.Fingerprint, &tagmanager.Folder{
				Name:  in.Name,
				Notes: in.Notes,
			})
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		case "delete":
			if err := client.DeleteFolder(ctx, in.AccountID, in.ContainerID, in.WorkspaceID, in.FolderID, in.Confirm); err != nil {
				return nil, nil, err
			}
			return textJSONResult(map[string]any{"deleted": true, "folderId": in.FolderID})
		case "entities":
			res, err := client.ListFolderEntities(ctx, in.AccountID, in.ContainerID, in.WorkspaceID, in.FolderID)
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		default:
			return nil, nil, fmt.Errorf("unsupported action %q for gtm_folder", in.Action)
		}
	})

	type templateIn struct {
		Action                 string `json:"action" jsonschema:"Operation: list, get, or import_gallery"`
		AccountID              string `json:"accountId" jsonschema:"GTM Account ID"`
		ContainerID            string `json:"containerId" jsonschema:"GTM Container ID"`
		WorkspaceID            string `json:"workspaceId" jsonschema:"GTM Workspace ID"`
		TemplateID             string `json:"templateId,omitempty" jsonschema:"Custom Template ID (required for get)"`
		Owner                  string `json:"owner,omitempty" jsonschema:"Community Gallery owner (e.g. stape-io) for import_gallery"`
		Repository             string `json:"repository,omitempty" jsonschema:"Community Gallery repository (e.g. fb-tag) for import_gallery"`
		SHA                    string `json:"sha,omitempty" jsonschema:"Optional commit SHA for import_gallery"`
		AcknowledgePermissions bool   `json:"acknowledgePermissions,omitempty" jsonschema:"Acknowledge template permissions on import_gallery"`
		Fresh                  bool   `json:"fresh,omitempty" jsonschema:"Bypass list snapshot cache"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "gtm_template",
		Description: "Inspect custom templates or import Community Template Gallery templates (list, get, import_gallery).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in templateIn) (*mcp.CallToolResult, any, error) {
		switch in.Action {
		case "list":
			res, err := client.ListTemplates(ctx, in.AccountID, in.ContainerID, in.WorkspaceID, gtm.ListOptions{Fresh: in.Fresh})
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		case "get":
			res, err := client.GetTemplate(ctx, in.AccountID, in.ContainerID, in.WorkspaceID, in.TemplateID)
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		case "import_gallery":
			res, err := client.ImportTemplateFromGallery(ctx, in.AccountID, in.ContainerID, in.WorkspaceID, gtm.ImportGalleryTemplateOptions{
				Owner:                  in.Owner,
				Repository:             in.Repository,
				SHA:                    in.SHA,
				AcknowledgePermissions: in.AcknowledgePermissions,
			})
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		default:
			return nil, nil, fmt.Errorf("unsupported action %q for gtm_template", in.Action)
		}
	})

	type clientIn struct {
		Action      string `json:"action" jsonschema:"Operation: list or get"`
		AccountID   string `json:"accountId" jsonschema:"GTM Account ID"`
		ContainerID string `json:"containerId" jsonschema:"GTM Container ID"`
		WorkspaceID string `json:"workspaceId" jsonschema:"GTM Workspace ID"`
		ClientID    string `json:"clientId,omitempty" jsonschema:"sGTM Client ID (required for get)"`
		Fresh       bool   `json:"fresh,omitempty" jsonschema:"Bypass list snapshot cache"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "gtm_client",
		Description: "Inspect Server-Side GTM clients (list or get).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in clientIn) (*mcp.CallToolResult, any, error) {
		switch in.Action {
		case "list":
			res, err := client.ListClients(ctx, in.AccountID, in.ContainerID, in.WorkspaceID, gtm.ListOptions{Fresh: in.Fresh})
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		case "get":
			res, err := client.GetClient(ctx, in.AccountID, in.ContainerID, in.WorkspaceID, in.ClientID)
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		default:
			return nil, nil, fmt.Errorf("unsupported action %q for gtm_client", in.Action)
		}
	})

	type transformationIn struct {
		Action           string `json:"action" jsonschema:"Operation: list or get"`
		AccountID        string `json:"accountId" jsonschema:"GTM Account ID"`
		ContainerID      string `json:"containerId" jsonschema:"GTM Container ID"`
		WorkspaceID      string `json:"workspaceId" jsonschema:"GTM Workspace ID"`
		TransformationID string `json:"transformationId,omitempty" jsonschema:"sGTM Transformation ID (required for get)"`
		Fresh            bool   `json:"fresh,omitempty" jsonschema:"Bypass list snapshot cache"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "gtm_transformation",
		Description: "Inspect Server-Side GTM transformations (list or get).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in transformationIn) (*mcp.CallToolResult, any, error) {
		switch in.Action {
		case "list":
			res, err := client.ListTransformations(ctx, in.AccountID, in.ContainerID, in.WorkspaceID, gtm.ListOptions{Fresh: in.Fresh})
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		case "get":
			res, err := client.GetTransformation(ctx, in.AccountID, in.ContainerID, in.WorkspaceID, in.TransformationID)
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		default:
			return nil, nil, fmt.Errorf("unsupported action %q for gtm_transformation", in.Action)
		}
	})
}

func registerVersionTools(srv *mcp.Server, client *gtm.Client) {
	type versionIn struct {
		Action         string `json:"action" jsonschema:"Operation: list_headers, live, get, or publish"`
		AccountID      string `json:"accountId" jsonschema:"GTM Account ID"`
		ContainerID    string `json:"containerId" jsonschema:"GTM Container ID"`
		VersionID      string `json:"versionId,omitempty" jsonschema:"GTM Container Version ID (required for get and publish)"`
		Fingerprint    string `json:"fingerprint,omitempty" jsonschema:"Version fingerprint (required for publish)"`
		IncludeDeleted bool   `json:"includeDeleted,omitempty" jsonschema:"Include deleted versions in list_headers"`
		Confirm        bool   `json:"confirm,omitempty" jsonschema:"Must be true for publish (and server must run in full mode)"`
		Fresh          bool   `json:"fresh,omitempty" jsonschema:"Bypass list snapshot cache"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "gtm_version",
		Description: "Inspect container versions or publish a version (list_headers, live, get, publish). Publishing requires full mode, confirm=true, and fingerprint.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in versionIn) (*mcp.CallToolResult, any, error) {
		switch in.Action {
		case "list_headers":
			res, err := client.ListVersionHeaders(ctx, in.AccountID, in.ContainerID, in.IncludeDeleted, gtm.ListOptions{Fresh: in.Fresh})
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		case "live":
			res, err := client.GetLiveVersion(ctx, in.AccountID, in.ContainerID)
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		case "get":
			res, err := client.GetVersion(ctx, in.AccountID, in.ContainerID, in.VersionID)
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		case "publish":
			res, err := client.PublishVersion(ctx, in.AccountID, in.ContainerID, in.VersionID, in.Fingerprint, in.Confirm)
			if err != nil {
				return nil, nil, err
			}
			return textJSONResult(res)
		default:
			return nil, nil, fmt.Errorf("unsupported action %q for gtm_version", in.Action)
		}
	})
}

func registerBlueprintTools(srv *mcp.Server, client *gtm.Client, reg *blueprints.Registry) {
	type bpListIn struct{}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "gtm_blueprint_list",
		Description: "List all official declarative GTM tracking blueprints (Google Tag/GA4, Lead Gen, Ecommerce, Google Ads Enhanced Conversions, Consent Mode v2, Meta Pixel Web, Meta CAPI sGTM).",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ bpListIn) (*mcp.CallToolResult, any, error) {
		return textJSONResult(reg.List())
	})

	type bpPlanIn struct {
		AccountID   string            `json:"accountId" jsonschema:"GTM Account ID"`
		ContainerID string            `json:"containerId" jsonschema:"GTM Container ID"`
		WorkspaceID string            `json:"workspaceId" jsonschema:"GTM Workspace ID"`
		BlueprintID string            `json:"blueprintId" jsonschema:"Blueprint ID (from gtm_blueprint_list)"`
		Params      map[string]string `json:"params,omitempty" jsonschema:"Blueprint input parameter key-value pairs"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "gtm_blueprint_plan",
		Description: "Dry-run an official blueprint against a target workspace: computes diff, skips existing entities, checks required gallery templates/built-ins, and outputs dataLayer snippets without mutating GTM.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in bpPlanIn) (*mcp.CallToolResult, any, error) {
		res, err := blueprints.Plan(ctx, client, reg, blueprints.PlanInput{
			AccountID:   in.AccountID,
			ContainerID: in.ContainerID,
			WorkspaceID: in.WorkspaceID,
			BlueprintID: in.BlueprintID,
			Params:      in.Params,
		})
		if err != nil {
			return nil, nil, err
		}
		return textJSONResult(res)
	})

	type bpApplyIn struct {
		AccountID                      string            `json:"accountId" jsonschema:"GTM Account ID"`
		ContainerID                    string            `json:"containerId" jsonschema:"GTM Container ID"`
		WorkspaceID                    string            `json:"workspaceId,omitempty" jsonschema:"Target Workspace ID (optional if createWorkspaceName is provided)"`
		BlueprintID                    string            `json:"blueprintId" jsonschema:"Blueprint ID (from gtm_blueprint_list)"`
		Params                         map[string]string `json:"params,omitempty" jsonschema:"Blueprint input parameter key-value pairs"`
		CreateWorkspaceName            string            `json:"createWorkspaceName,omitempty" jsonschema:"Create a dedicated workspace with this name before applying"`
		ImportGalleryTemplates         bool              `json:"importGalleryTemplates,omitempty" jsonschema:"Automatically import missing Community Template Gallery templates"`
		AcknowledgeTemplatePermissions bool              `json:"acknowledgeTemplatePermissions,omitempty" jsonschema:"Acknowledge gallery template permissions when importing"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "gtm_blueprint_apply",
		Description: "Apply an official blueprint atomically to a GTM workspace (optionally creating a dedicated workspace, importing required gallery templates, enabling built-ins, and executing workspace bulkUpdate).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in bpApplyIn) (*mcp.CallToolResult, any, error) {
		res, err := blueprints.Apply(ctx, client, reg, blueprints.ApplyInput{
			PlanInput: blueprints.PlanInput{
				AccountID:   in.AccountID,
				ContainerID: in.ContainerID,
				WorkspaceID: in.WorkspaceID,
				BlueprintID: in.BlueprintID,
				Params:      in.Params,
			},
			CreateWorkspaceName:            in.CreateWorkspaceName,
			ImportGalleryTemplates:         in.ImportGalleryTemplates,
			AcknowledgeTemplatePermissions: in.AcknowledgeTemplatePermissions,
		})
		if err != nil {
			return nil, nil, err
		}
		return textJSONResult(res)
	})
}

func registerAuditAndPlanTools(srv *mcp.Server, client *gtm.Client) {
	type auditIn struct {
		AccountID   string `json:"accountId" jsonschema:"GTM Account ID"`
		ContainerID string `json:"containerId" jsonschema:"GTM Container ID"`
		WorkspaceID string `json:"workspaceId,omitempty" jsonschema:"GTM Workspace ID (optional when live=true)"`
		Live        bool   `json:"live,omitempty" jsonschema:"Audit the published live container version instead of a workspace"`
		MinSeverity string `json:"minSeverity,omitempty" jsonschema:"Minimum severity to return: critical, high, medium, or low (default low)"`
		Format      string `json:"format,omitempty" jsonschema:"Output format: json (default) or markdown"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "gtm_audit",
		Description: "Run deterministic privacy, security, Consent Mode v2, Meta deduplication, and hygiene audit on a GTM workspace or live container version.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in auditIn) (*mcp.CallToolResult, any, error) {
		sev, err := auditor.ParseSeverity(in.MinSeverity)
		if err != nil {
			return nil, nil, err
		}
		rep, err := auditor.Run(ctx, client, auditor.Input{
			AccountID:   in.AccountID,
			ContainerID: in.ContainerID,
			WorkspaceID: in.WorkspaceID,
			Live:        in.Live,
			MinSeverity: sev,
		})
		if err != nil {
			return nil, nil, err
		}
		if strings.EqualFold(strings.TrimSpace(in.Format), "markdown") {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: rep.RenderMarkdown()}},
			}, nil, nil
		}
		return textJSONResult(rep)
	})

	type trackingPlanIn struct {
		AccountID     string `json:"accountId" jsonschema:"GTM Account ID"`
		ContainerID   string `json:"containerId" jsonschema:"GTM Container ID"`
		WorkspaceID   string `json:"workspaceId,omitempty" jsonschema:"GTM Workspace ID (optional when live=true)"`
		Live          bool   `json:"live,omitempty" jsonschema:"Generate tracking plan from the published live version"`
		SnippetTarget string `json:"snippetTarget,omitempty" jsonschema:"Code snippet flavor: ts (default), js, nextjs, or shopify"`
		Format        string `json:"format,omitempty" jsonschema:"Output format: markdown (default) or json"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "gtm_tracking_plan",
		Description: "Generate a living GTM tracking plan (events, destinations, Data Layer dictionary, and typed application snippets) from a workspace or live version.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in trackingPlanIn) (*mcp.CallToolResult, any, error) {
		target, err := trackingplan.ParseSnippetTarget(in.SnippetTarget)
		if err != nil {
			return nil, nil, err
		}
		plan, err := trackingplan.Generate(ctx, client, trackingplan.Input{
			AccountID:     in.AccountID,
			ContainerID:   in.ContainerID,
			WorkspaceID:   in.WorkspaceID,
			Live:          in.Live,
			SnippetTarget: target,
		})
		if err != nil {
			return nil, nil, err
		}
		if strings.EqualFold(strings.TrimSpace(in.Format), "json") {
			return textJSONResult(plan)
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: plan.RenderMarkdown()}},
		}, nil, nil
	})
}
