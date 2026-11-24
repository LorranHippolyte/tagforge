package trackingplan

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LorranHippolyte/tagforge/internal/auditor"
	"github.com/LorranHippolyte/tagforge/internal/gtm"
	"github.com/LorranHippolyte/tagforge/internal/safety"
	"google.golang.org/api/option"
	"google.golang.org/api/tagmanager/v2"
)

func sampleSnapshot() *auditor.Snapshot {
	return &auditor.Snapshot{
		AccountID:   "1",
		ContainerID: "2",
		WorkspaceID: "3",
		TargetLabel: "workspace 3",
		Templates: []*tagmanager.CustomTemplate{
			{
				TemplateId: "50",
				Name:       "Facebook Pixel by Stape",
				GalleryReference: &tagmanager.GalleryReference{
					Owner:      "stape-io",
					Repository: "fb-tag",
				},
			},
		},
		Variables: []*tagmanager.Variable{
			{VariableId: "1", Name: "CONST - GA4 ID", Type: "c", Parameter: []*tagmanager.Parameter{{Key: "value", Value: "G-PLAN123"}}},
			{VariableId: "2", Name: "CONST - Meta CAPI Access Token", Type: "c", Parameter: []*tagmanager.Parameter{{Key: "value", Value: "EAAB_SECRET_NEVER_PRINT"}}},
			{VariableId: "3", Name: "DLV - form_name", Type: "v", Parameter: []*tagmanager.Parameter{{Key: "name", Value: "form_name"}}},
		},
		Triggers: []*tagmanager.Trigger{
			{
				TriggerId: "10",
				Name:      "CE - GA4 Ecommerce",
				Type:      "customEvent",
				CustomEventFilter: []*tagmanager.Condition{
					{
						Type: "matchRegex",
						Parameter: []*tagmanager.Parameter{
							{Key: "arg0", Value: "{{_event}}"},
							{Key: "arg1", Value: "^(view_item|add_to_cart|purchase)$"},
						},
					},
				},
			},
			{
				TriggerId: "11",
				Name:      "CE - generate_lead",
				Type:      "customEvent",
				CustomEventFilter: []*tagmanager.Condition{
					{
						Type: "equals",
						Parameter: []*tagmanager.Parameter{
							{Key: "arg0", Value: "{{_event}}"},
							{Key: "arg1", Value: "generate_lead"},
						},
					},
				},
			},
		},
		Tags: []*tagmanager.Tag{
			{
				TagId:           "100",
				Name:            "GA4 - Ecommerce",
				Type:            "gaawe",
				FiringTriggerId: []string{"10"},
				Parameter:       []*tagmanager.Parameter{{Key: "measurementIdOverride", Value: "{{CONST - GA4 ID}}"}},
			},
			{
				TagId:           "101",
				Name:            "GA4 - Lead",
				Type:            "gaawe",
				FiringTriggerId: []string{"11"},
				Parameter: []*tagmanager.Parameter{
					{Key: "eventName", Value: "generate_lead"},
					{Key: "form_name", Value: "{{DLV - form_name}}"},
				},
			},
			{
				TagId:           "102",
				Name:            "Meta Pixel - Purchase",
				Type:            "cvt_2_50",
				FiringTriggerId: []string{"10"},
				ConsentSettings: &tagmanager.TagConsentSetting{
					ConsentStatus: "needed",
					ConsentType:   &tagmanager.Parameter{Type: "LIST", List: []*tagmanager.Parameter{{Value: "ad_storage"}}},
				},
			},
		},
	}
}

func TestBuildFromSnapshotRedactsSecretsAndExtractsEvents(t *testing.T) {
	snap := sampleSnapshot()

	for _, target := range []SnippetTarget{TargetTS, TargetJS, TargetNextJS, TargetShopify} {
		plan := BuildFromSnapshot(snap, target)
		if len(plan.Events) != 4 { // view_item, add_to_cart, purchase, generate_lead
			t.Fatalf("target=%s expected 4 expanded events, got %d", target, len(plan.Events))
		}
		if len(plan.DataLayerVars) != 1 || plan.DataLayerVars[0].KeyPath != "form_name" {
			t.Fatalf("unexpected DataLayerVars: %+v", plan.DataLayerVars)
		}
		md := plan.RenderMarkdown()
		if strings.Contains(md, "EAAB_SECRET_NEVER_PRINT") {
			t.Fatalf("secret constant leaked into Markdown tracking plan")
		}
		if !strings.Contains(md, "[REDACTED]") {
			t.Fatalf("expected [REDACTED] constant in Markdown")
		}
		if !strings.Contains(md, "Meta Pixel") || !strings.Contains(md, "GA4") {
			t.Fatalf("expected platforms in Markdown: %s", md)
		}
		if strings.TrimSpace(plan.Snippets) == "" {
			t.Fatalf("expected non-empty snippets for target %s", target)
		}
	}
}

func TestGenerateAgainstMockServerWorkspaceAndLive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/versions:live"):
			_ = json.NewEncoder(w).Encode(&tagmanager.ContainerVersion{
				ContainerVersionId: "9",
				Name:               "Prod v9",
				Tag:                sampleSnapshot().Tags,
				Trigger:            sampleSnapshot().Triggers,
				Variable:           sampleSnapshot().Variables,
				CustomTemplate:     sampleSnapshot().Templates,
			})
		case strings.HasSuffix(r.URL.Path, "/tags"):
			_ = json.NewEncoder(w).Encode(map[string]any{"tag": sampleSnapshot().Tags})
		case strings.HasSuffix(r.URL.Path, "/triggers"):
			_ = json.NewEncoder(w).Encode(map[string]any{"trigger": sampleSnapshot().Triggers})
		case strings.HasSuffix(r.URL.Path, "/variables"):
			_ = json.NewEncoder(w).Encode(map[string]any{"variable": sampleSnapshot().Variables})
		case strings.HasSuffix(r.URL.Path, "/templates"):
			_ = json.NewEncoder(w).Encode(map[string]any{"template": sampleSnapshot().Templates})
		case strings.HasSuffix(r.URL.Path, "/workspaces"),
			strings.HasSuffix(r.URL.Path, "/version_headers"),
			strings.HasSuffix(r.URL.Path, "/built_in_variables"),
			strings.HasSuffix(r.URL.Path, "/folders"),
			strings.HasSuffix(r.URL.Path, "/clients"),
			strings.HasSuffix(r.URL.Path, "/transformations"):
			_ = json.NewEncoder(w).Encode(map[string]any{})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client, err := gtm.NewClient(context.Background(), gtm.ClientConfig{
		ClientOptions: []option.ClientOption{
			option.WithHTTPClient(srv.Client()),
			option.WithEndpoint(srv.URL),
			option.WithoutAuthentication(),
		},
		Policy:    &safety.Policy{Mode: safety.ModeReadOnly},
		Scheduler: gtm.NewScheduler(gtm.SchedulerOptions{QPS: 100, Burst: 20, MaxAttempts: 2, BaseBackoff: time.Millisecond}),
		Cache:     gtm.NewSnapshotCache(false, time.Minute),
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	wsPlan, err := Generate(context.Background(), client, Input{
		AccountID: "1", ContainerID: "2", WorkspaceID: "3", SnippetTarget: TargetNextJS,
	})
	if err != nil {
		t.Fatalf("workspace Generate failed: %v", err)
	}
	if !strings.Contains(wsPlan.Snippets, "'use client'") {
		t.Fatalf("expected Next.js snippet output: %s", wsPlan.Snippets)
	}

	livePlan, err := Generate(context.Background(), client, Input{
		AccountID: "1", ContainerID: "2", Live: true, SnippetTarget: TargetShopify,
	})
	if err != nil {
		t.Fatalf("live Generate failed: %v", err)
	}
	if livePlan.LiveVersionID != "9" || !strings.Contains(livePlan.Snippets, "analytics.subscribe") {
		t.Fatalf("unexpected live plan: %+v", livePlan)
	}
	for _, want := range []string{"customerPrivacy", "visitorConsentCollected", "event?.context?.document", "shopify_page_view"} {
		if !strings.Contains(livePlan.Snippets, want) {
			t.Fatalf("upgraded Shopify snippet missing %q:\n%s", want, livePlan.Snippets)
		}
	}
}

func TestInferPlatformSupportsPhase4Vendors(t *testing.T) {
	snap := &auditor.Snapshot{
		AccountID:   "1",
		ContainerID: "2",
		WorkspaceID: "3",
		TargetLabel: "workspace 3",
		Templates: []*tagmanager.CustomTemplate{
			{TemplateId: "61", Name: "TikTok Web", GalleryReference: &tagmanager.GalleryReference{Owner: "stape-io", Repository: "tiktok-web-tag"}},
			{TemplateId: "62", Name: "TikTok EAPI", GalleryReference: &tagmanager.GalleryReference{Owner: "tiktok", Repository: "gtm-template-eapi"}},
			{TemplateId: "63", Name: "LinkedIn Insight", GalleryReference: &tagmanager.GalleryReference{Owner: "linkedin", Repository: "linkedin-gtm-community-template"}},
			{TemplateId: "64", Name: "LinkedIn CAPI", GalleryReference: &tagmanager.GalleryReference{Owner: "linkedin-developers", Repository: "linkedin-capi-tag-template"}},
			{TemplateId: "65", Name: "Microsoft Clarity", GalleryReference: &tagmanager.GalleryReference{Owner: "microsoft", Repository: "clarity-gtm-template"}},
			{TemplateId: "66", Name: "Pinterest", GalleryReference: &tagmanager.GalleryReference{Owner: "pinterest", Repository: "ws-gtm-template"}},
		},
		Triggers: []*tagmanager.Trigger{
			{TriggerId: "1", Name: "PV - All Pages", Type: "pageview"},
		},
		Tags: []*tagmanager.Tag{
			{TagId: "1", Name: "TikTok Web", Type: "cvt_2_61", FiringTriggerId: []string{"1"}},
			{TagId: "2", Name: "TikTok EAPI", Type: "cvt_2_62", FiringTriggerId: []string{"1"}},
			{TagId: "3", Name: "LinkedIn Insight", Type: "cvt_2_63", FiringTriggerId: []string{"1"}},
			{TagId: "4", Name: "LinkedIn CAPI", Type: "cvt_2_64", FiringTriggerId: []string{"1"}},
			{TagId: "5", Name: "Clarity", Type: "cvt_2_65", FiringTriggerId: []string{"1"}},
			{TagId: "6", Name: "Pinterest Gallery", Type: "cvt_2_66", FiringTriggerId: []string{"1"}},
			{TagId: "7", Name: "Microsoft UET", Type: "baut", FiringTriggerId: []string{"1"}},
			{TagId: "8", Name: "Shopify sGTM GA4", Type: "sgtmgaaw", FiringTriggerId: []string{"1"}},
		},
	}

	md := BuildFromSnapshot(snap, TargetShopify).RenderMarkdown()
	for _, want := range []string{
		"TikTok Pixel",
		"TikTok Events API (sGTM)",
		"LinkedIn Insight Tag",
		"LinkedIn CAPI (sGTM)",
		"Microsoft Clarity",
		"Pinterest Tag",
		"Microsoft Advertising UET",
		"GA4 (sGTM)",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("tracking plan markdown missing platform %q:\n%s", want, md)
		}
	}
}
