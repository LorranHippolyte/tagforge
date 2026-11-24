package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/LorranHippolyte/tagforge/internal/blueprints"
	"github.com/LorranHippolyte/tagforge/internal/gtm"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func registerResources(srv *mcp.Server, client *gtm.Client, reg *blueprints.Registry) {
	srv.AddResource(&mcp.Resource{
		Name:        "guides_index",
		Title:       "GTM Best-Practice Guides",
		Description: "Index of official GTM guidance topics served by tagforge.",
		URI:         "gtm://guides",
		MIMEType:    "application/json",
	}, func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		topics := GuideTopics()
		items := make([]map[string]string, 0, len(topics))
		for _, t := range topics {
			items = append(items, map[string]string{
				"topic": t,
				"uri":   "gtm://guides/" + t,
			})
		}
		return jsonResourceResult(req.Params.URI, items)
	})

	srv.AddResourceTemplate(&mcp.ResourceTemplate{
		Name:        "guide_topic",
		Title:       "GTM Guidance Document",
		Description: "Vendor-accurate GTM guidance document by topic name.",
		URITemplate: "gtm://guides/{topic}",
		MIMEType:    "text/markdown",
	}, func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		u, err := url.Parse(req.Params.URI)
		if err != nil || u.Scheme != "gtm" || u.Host != "guides" {
			return nil, mcp.ResourceNotFoundError(req.Params.URI)
		}
		topic := strings.Trim(u.Path, "/")
		text, ok := Guides[topic]
		if !ok {
			return nil, mcp.ResourceNotFoundError(req.Params.URI)
		}
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{{
				URI:      req.Params.URI,
				MIMEType: "text/markdown",
				Text:     text,
			}},
		}, nil
	})

	srv.AddResource(&mcp.Resource{
		Name:        "blueprints_index",
		Title:       "Official GTM Blueprints",
		Description: "Index of embedded official GTM tracking blueprints.",
		URI:         "gtm://blueprints",
		MIMEType:    "application/json",
	}, func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		specs := reg.List()
		items := make([]map[string]string, 0, len(specs))
		for _, s := range specs {
			items = append(items, map[string]string{
				"id":       s.ID,
				"name":     s.Name,
				"context":  s.Context,
				"category": s.Category,
				"uri":      "gtm://blueprints/" + s.ID,
			})
		}
		return jsonResourceResult(req.Params.URI, items)
	})

	srv.AddResourceTemplate(&mcp.ResourceTemplate{
		Name:        "blueprint_spec",
		Title:       "Official Blueprint Specification",
		Description: "Full declarative specification and parameters for one embedded blueprint.",
		URITemplate: "gtm://blueprints/{id}",
		MIMEType:    "application/json",
	}, func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		u, err := url.Parse(req.Params.URI)
		if err != nil || u.Scheme != "gtm" || u.Host != "blueprints" {
			return nil, mcp.ResourceNotFoundError(req.Params.URI)
		}
		id := strings.Trim(u.Path, "/")
		spec, ok := reg.Get(id)
		if !ok {
			return nil, mcp.ResourceNotFoundError(req.Params.URI)
		}
		return jsonResourceResult(req.Params.URI, spec)
	})

	srv.AddResource(&mcp.Resource{
		Name:        "gtm_accounts",
		Title:       "Accessible GTM Accounts",
		Description: "Read-only list of Google Tag Manager accounts accessible with current credentials.",
		URI:         "gtm://accounts",
		MIMEType:    "application/json",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		accounts, err := client.ListAccounts(ctx, gtm.ListOptions{})
		if err != nil {
			return nil, err
		}
		return jsonResourceResult(req.Params.URI, accounts)
	})

	srv.AddResourceTemplate(&mcp.ResourceTemplate{
		Name:        "gtm_workspace_summary",
		Title:       "GTM Workspace Summary",
		Description: "Compact read-only inventory of tags, triggers, variables, and templates in a GTM workspace.",
		URITemplate: "gtm://accounts/{accountId}/containers/{containerId}/workspaces/{workspaceId}/summary",
		MIMEType:    "application/json",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		u, err := url.Parse(req.Params.URI)
		if err != nil || u.Scheme != "gtm" || u.Host != "accounts" {
			return nil, mcp.ResourceNotFoundError(req.Params.URI)
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) != 6 || parts[1] != "containers" || parts[3] != "workspaces" || parts[5] != "summary" {
			return nil, mcp.ResourceNotFoundError(req.Params.URI)
		}
		accountID, containerID, workspaceID := parts[0], parts[2], parts[4]

		tags, err := client.ListTags(ctx, accountID, containerID, workspaceID, gtm.ListOptions{})
		if err != nil {
			return nil, err
		}
		triggers, err := client.ListTriggers(ctx, accountID, containerID, workspaceID, gtm.ListOptions{})
		if err != nil {
			return nil, err
		}
		variables, err := client.ListVariables(ctx, accountID, containerID, workspaceID, gtm.ListOptions{})
		if err != nil {
			return nil, err
		}

		tagSummaries := make([]map[string]any, 0, len(tags))
		for _, tg := range tags {
			tagSummaries = append(tagSummaries, map[string]any{
				"id": tg.TagId, "name": tg.Name, "type": tg.Type, "paused": tg.Paused,
			})
		}
		trigSummaries := make([]map[string]any, 0, len(triggers))
		for _, tr := range triggers {
			trigSummaries = append(trigSummaries, map[string]any{
				"id": tr.TriggerId, "name": tr.Name, "type": tr.Type,
			})
		}
		varSummaries := make([]map[string]any, 0, len(variables))
		for _, v := range variables {
			varSummaries = append(varSummaries, map[string]any{
				"id": v.VariableId, "name": v.Name, "type": v.Type,
			})
		}

		return jsonResourceResult(req.Params.URI, map[string]any{
			"accountId":   accountID,
			"containerId": containerID,
			"workspaceId": workspaceID,
			"counts": map[string]int{
				"tags":      len(tags),
				"triggers":  len(triggers),
				"variables": len(variables),
			},
			"tags":      tagSummaries,
			"triggers":  trigSummaries,
			"variables": varSummaries,
		})
	})
}

func registerPrompts(srv *mcp.Server) {
	srv.AddPrompt(&mcp.Prompt{
		Name:        "audit_container",
		Title:       "Audit GTM Container",
		Description: "Run a full privacy, security, reliability, and hygiene audit on a GTM workspace or live container.",
		Arguments: []*mcp.PromptArgument{
			{Name: "accountId", Description: "GTM Account ID", Required: true},
			{Name: "containerId", Description: "GTM Container ID", Required: true},
			{Name: "workspaceId", Description: "GTM Workspace ID (leave empty to audit live version)", Required: false},
		},
	}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		args := req.Params.Arguments
		targetDesc := "live version (set live=true)"
		if ws := strings.TrimSpace(args["workspaceId"]); ws != "" {
			targetDesc = fmt.Sprintf("workspaceId=%q", ws)
		}
		text := fmt.Sprintf(
			"Audit GTM accountId=%q containerId=%q (%s).\n"+
				"1. Call 'gtm_audit' with format='json' (or 'markdown') first.\n"+
				"2. Group findings by severity (critical -> low), citing entity IDs and concrete fixes.\n"+
				"3. Do not mutate GTM until the user approves the remediation plan; when approved, work in a dedicated workspace via 'gtm_workspace' 'create'.",
			args["accountId"], args["containerId"], targetDesc,
		)
		return userPromptResult("Audit GTM container safely", text), nil
	})

	srv.AddPrompt(&mcp.Prompt{
		Name:        "generate_tracking_plan",
		Title:       "Generate Living Tracking Plan",
		Description: "Extract events, destinations, Data Layer keys, and typed snippets from a GTM container.",
		Arguments: []*mcp.PromptArgument{
			{Name: "accountId", Description: "GTM Account ID", Required: true},
			{Name: "containerId", Description: "GTM Container ID", Required: true},
			{Name: "workspaceId", Description: "GTM Workspace ID (omit for live)", Required: false},
			{Name: "snippetTarget", Description: "ts, js, nextjs, or shopify", Required: false},
		},
	}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		args := req.Params.Arguments
		target := args["snippetTarget"]
		if target == "" {
			target = "ts"
		}
		text := fmt.Sprintf(
			"Generate a living tracking plan for accountId=%q containerId=%q (workspaceId=%q, snippetTarget=%q).\n"+
				"Call 'gtm_tracking_plan' (set live=true if workspaceId is empty) with format='markdown', verify no secret constants were exposed, and present the event matrix + %s code snippets cleanly.",
			args["accountId"], args["containerId"], args["workspaceId"], target, target,
		)
		return userPromptResult("Generate living GTM tracking plan", text), nil
	})

	srv.AddPrompt(&mcp.Prompt{
		Name:        "setup_ecommerce",
		Title:       "Setup Official GA4 + Meta Ecommerce Tracking",
		Description: "Plan and safely deploy official GA4 Ecommerce, Consent Mode v2, and optional Meta Pixel Web blueprints.",
		Arguments: []*mcp.PromptArgument{
			{Name: "accountId", Description: "GTM Account ID", Required: true},
			{Name: "containerId", Description: "GTM Container ID", Required: true},
			{Name: "measurementId", Description: "GA4 Measurement ID (G-...)", Required: true},
			{Name: "pixelId", Description: "Optional Meta Pixel ID", Required: false},
		},
	}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		args := req.Params.Arguments
		text := fmt.Sprintf(
			"Set up official ecommerce tracking on accountId=%q containerId=%q with measurement_id=%q and optional pixel_id=%q.\n"+
				"1. Call 'gtm_blueprint_plan' (dry-run) for 'consent-mode-v2', 'google-tag-ga4-base', 'google-ga4-ecommerce' (and 'meta-pixel-web' if pixelId is provided).\n"+
				"2. Present the planned changes and dataLayer contract to the user for approval.\n"+
				"3. After approval, apply inside a dedicated workspace using 'gtm_blueprint_apply' with createWorkspaceName='feat-ecommerce-tracking' and importGalleryTemplates=true (for Meta).\n"+
				"4. Finish by running 'gtm_audit' and 'gtm_workspace' 'preview' to verify zero errors before version creation.",
			args["accountId"], args["containerId"], args["measurementId"], args["pixelId"],
		)
		return userPromptResult("Plan official ecommerce setup", text), nil
	})

	srv.AddPrompt(&mcp.Prompt{
		Name:        "setup_leadgen",
		Title:       "Setup Official Lead Generation Tracking",
		Description: "Plan and safely deploy Consent Mode v2, GA4 Lead Gen, Google Ads Enhanced Conversions, and optional Meta Pixel.",
		Arguments: []*mcp.PromptArgument{
			{Name: "accountId", Description: "GTM Account ID", Required: true},
			{Name: "containerId", Description: "GTM Container ID", Required: true},
			{Name: "measurementId", Description: "GA4 Measurement ID (G-...)", Required: true},
			{Name: "conversionId", Description: "Optional Google Ads Conversion ID", Required: false},
			{Name: "pixelId", Description: "Optional Meta Pixel ID", Required: false},
		},
	}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		args := req.Params.Arguments
		text := fmt.Sprintf(
			"Set up lead-generation tracking on accountId=%q containerId=%q (GA4=%q, AdsConversionId=%q, MetaPixel=%q).\n"+
				"Plan with 'gtm_blueprint_plan' ('consent-mode-v2', 'google-tag-ga4-base', 'google-ga4-leadgen', plus 'google-ads-conversions' / 'meta-pixel-web' when IDs are supplied), review with the user, then apply into a dedicated workspace and verify with 'gtm_audit'.",
			args["accountId"], args["containerId"], args["measurementId"], args["conversionId"], args["pixelId"],
		)
		return userPromptResult("Plan official lead-gen setup", text), nil
	})

	srv.AddPrompt(&mcp.Prompt{
		Name:        "plan_safe_release",
		Title:       "Plan Safe Workspace Release",
		Description: "Run pre-release checks (audit, workspace status, sync, quick preview, version creation) before publishing.",
		Arguments: []*mcp.PromptArgument{
			{Name: "accountId", Description: "GTM Account ID", Required: true},
			{Name: "containerId", Description: "GTM Container ID", Required: true},
			{Name: "workspaceId", Description: "GTM Workspace ID", Required: true},
		},
	}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		args := req.Params.Arguments
		text := fmt.Sprintf(
			"Prepare a safe release for accountId=%q containerId=%q workspaceId=%q:\n"+
				"1. Run 'gtm_audit' on the workspace; stop and report any critical/high findings (especially PII, missing consent, or active test_event_code).\n"+
				"2. Run 'gtm_workspace' 'status' and 'preview' to confirm zero merge conflicts and zero compiler errors.\n"+
				"3. Only after checks pass, run 'gtm_workspace' 'create_version' with descriptive name and notes.\n"+
				"4. NEVER publish without explicit human approval (publishing also requires server mode 'full' and confirm=true).",
			args["accountId"], args["containerId"], args["workspaceId"],
		)
		return userPromptResult("Pre-release verification workflow", text), nil
	})
}

func jsonResourceResult(uri string, v any) (*mcp.ReadResourceResult, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{{
			URI:      uri,
			MIMEType: "application/json",
			Text:     string(b),
		}},
	}, nil
}

func userPromptResult(description, text string) *mcp.GetPromptResult {
	return &mcp.GetPromptResult{
		Description: description,
		Messages: []*mcp.PromptMessage{{
			Role:    "user",
			Content: &mcp.TextContent{Text: text},
		}},
	}
}
