package blueprints

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LorranHippolyte/tagforge/internal/auditor"
	"github.com/LorranHippolyte/tagforge/internal/gtm"
	"github.com/LorranHippolyte/tagforge/internal/safety"
	"google.golang.org/api/option"
	"google.golang.org/api/tagmanager/v2"
)

func newMockBlueprintClient(t *testing.T, srv *httptest.Server, policy *safety.Policy) *gtm.Client {
	t.Helper()
	c, err := gtm.NewClient(context.Background(), gtm.ClientConfig{
		ClientOptions: []option.ClientOption{
			option.WithHTTPClient(srv.Client()),
			option.WithEndpoint(srv.URL),
			option.WithoutAuthentication(),
		},
		Policy: policy,
		Scheduler: gtm.NewScheduler(gtm.SchedulerOptions{
			QPS:         100,
			Burst:       20,
			MaxAttempts: 2,
			BaseBackoff: 2 * time.Millisecond,
		}),
		Cache: gtm.NewSnapshotCache(false, 30*time.Second),
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	return c
}

func TestEmbeddedRegistryLoadsAllOfficialBlueprints(t *testing.T) {
	reg, err := NewDefaultRegistry()
	if err != nil {
		t.Fatalf("NewDefaultRegistry failed: %v", err)
	}
	specs := reg.List()
	if len(specs) != 16 {
		t.Fatalf("expected 16 embedded blueprints, got %d", len(specs))
	}

	expectedIDs := []string{
		"consent-mode-v2",
		"google-ads-conversions",
		"google-ga4-ecommerce",
		"google-ga4-leadgen",
		"google-tag-ga4-base",
		"linkedin-capi-sgtm",
		"linkedin-insight-web",
		"meta-capi-sgtm",
		"meta-pixel-web",
		"microsoft-clarity-web",
		"microsoft-uet-web",
		"pinterest-tag-web",
		"shopify-gtm-web",
		"shopify-sgtm-pack",
		"tiktok-events-api-sgtm",
		"tiktok-pixel-web",
	}
	for i, want := range expectedIDs {
		if specs[i].ID != want {
			t.Fatalf("specs[%d].ID = %q, want %q", i, specs[i].ID, want)
		}
		for _, tg := range specs[i].Tags {
			if ForbiddenLegacyTypes[tg.Type] {
				t.Fatalf("blueprint %q contains forbidden legacy tag type %q", specs[i].ID, tg.Type)
			}
		}
	}
}

func TestPlanAllOfficialBlueprintsAndRedactSecrets(t *testing.T) {
	reg, err := NewDefaultRegistry()
	if err != nil {
		t.Fatalf("NewDefaultRegistry failed: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/built_in_variables"):
			_ = json.NewEncoder(w).Encode(map[string]any{"builtInVariable": []any{}})
		case strings.HasSuffix(r.URL.Path, "/templates"):
			_ = json.NewEncoder(w).Encode(map[string]any{"template": []any{}})
		case strings.HasSuffix(r.URL.Path, "/folders"):
			_ = json.NewEncoder(w).Encode(map[string]any{"folder": []any{}})
		case strings.HasSuffix(r.URL.Path, "/variables"):
			_ = json.NewEncoder(w).Encode(map[string]any{"variable": []any{}})
		case strings.HasSuffix(r.URL.Path, "/triggers"):
			_ = json.NewEncoder(w).Encode(map[string]any{"trigger": []any{}})
		case strings.HasSuffix(r.URL.Path, "/tags"):
			_ = json.NewEncoder(w).Encode(map[string]any{"tag": []any{}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := newMockBlueprintClient(t, srv, &safety.Policy{Mode: safety.ModeReadOnly})
	ctx := context.Background()

	cases := []struct {
		id         string
		params     map[string]string
		wantSubstr string
		secretKey  string
		secretVal  string
	}{
		{
			id:         "google-tag-ga4-base",
			params:     map[string]string{"measurement_id": "G-TEST1234", "server_container_url": "https://sgtm.example.com"},
			wantSubstr: `"type":"googtag"`,
		},
		{
			id:         "google-ga4-leadgen",
			params:     map[string]string{"measurement_id": "G-TEST1234"},
			wantSubstr: `"type":"gaawe"`,
		},
		{
			id:         "google-ga4-ecommerce",
			params:     map[string]string{"measurement_id": "G-TEST1234"},
			wantSubstr: `"key":"sendEcommerceData"`,
		},
		{
			id:         "google-ads-conversions",
			params:     map[string]string{"conversion_id": "123456789", "conversion_label": "AbCdEf"},
			wantSubstr: `"type":"awec"`,
		},
		{
			id:         "consent-mode-v2",
			params:     map[string]string{"regions": "BR,EU"},
			wantSubstr: `"type":"consentInit"`,
		},
		{
			id:         "meta-pixel-web",
			params:     map[string]string{"pixel_id": "9988776655"},
			wantSubstr: `"consentStatus":"needed"`,
		},
		{
			id:         "meta-capi-sgtm",
			params:     map[string]string{"pixel_id": "9988776655", "access_token": "EAAB_SECRET_TOKEN_XYZ"},
			wantSubstr: `"serverEventDataList"`,
			secretKey:  "access_token",
			secretVal:  "EAAB_SECRET_TOKEN_XYZ",
		},
		{
			id:         "tiktok-pixel-web",
			params:     map[string]string{"pixel_id": "CD9079RC77U0N3GBV16Y"},
			wantSubstr: `"eventNameStandard"`,
		},
		{
			id:         "tiktok-events-api-sgtm",
			params:     map[string]string{"pixel_id": "CD9079RC77U0N3GBV16Y", "access_token": "TT_SECRET_TOKEN_XYZ"},
			wantSubstr: `"object_property_source"`,
			secretKey:  "access_token",
			secretVal:  "TT_SECRET_TOKEN_XYZ",
		},
		{
			id:         "linkedin-insight-web",
			params:     map[string]string{"partner_id": "1234567", "conversion_id": "7654321"},
			wantSubstr: `"conversionId"`,
		},
		{
			id:         "linkedin-capi-sgtm",
			params:     map[string]string{"access_token": "LI_SECRET_TOKEN_XYZ", "conversion_rule_urn": "urn:lla:llaPartnerConversion:1234567"},
			wantSubstr: `"conversionRuleUrn"`,
			secretKey:  "access_token",
			secretVal:  "LI_SECRET_TOKEN_XYZ",
		},
		{
			id:         "microsoft-clarity-web",
			params:     map[string]string{"project_id": "abc123def"},
			wantSubstr: `"analytics_storage"`,
		},
		{
			id:         "microsoft-uet-web",
			params:     map[string]string{"uet_tag_id": "99887766"},
			wantSubstr: `"type":"baut"`,
		},
		{
			id:         "pinterest-tag-web",
			params:     map[string]string{"tag_id": "2612345678901"},
			wantSubstr: `"useGA4Ecommerce"`,
		},
		{
			id:         "shopify-gtm-web",
			params:     map[string]string{"measurement_id": "G-SHOP1234", "server_container_url": "https://metrics.store.com"},
			wantSubstr: `"shopify_page_view"`,
		},
		{
			id:         "shopify-sgtm-pack",
			params:     map[string]string{"measurement_id": "G-SHOP1234"},
			wantSubstr: `"type":"sgtmgaaw"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			plan, err := Plan(ctx, client, reg, PlanInput{
				AccountID:   "1",
				ContainerID: "2",
				WorkspaceID: "3",
				BlueprintID: tc.id,
				Params:      tc.params,
			})
			if err != nil {
				t.Fatalf("Plan(%s) failed: %v", tc.id, err)
			}
			raw, err := json.Marshal(plan)
			if err != nil {
				t.Fatalf("Marshal plan failed: %v", err)
			}
			s := string(raw)
			if !strings.Contains(s, tc.wantSubstr) {
				t.Fatalf("plan JSON missing %q: %s", tc.wantSubstr, s)
			}
			if tc.secretKey != "" {
				if strings.Contains(s, tc.secretVal) {
					t.Fatalf("%s leaked into public PlanResult JSON!", tc.secretKey)
				}
				if plan.ResolvedParams[tc.secretKey] != "[REDACTED]" {
					t.Fatalf("expected [REDACTED] in ResolvedParams[%q], got %q", tc.secretKey, plan.ResolvedParams[tc.secretKey])
				}
			}
		})
	}
}

func TestPlanIdempotencySkipsExistingAndReusesFolderID(t *testing.T) {
	reg, err := NewDefaultRegistry()
	if err != nil {
		t.Fatalf("NewDefaultRegistry failed: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/built_in_variables"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"builtInVariable": []*tagmanager.BuiltInVariable{{Type: "event"}, {Type: "pageUrl"}},
			})
		case strings.HasSuffix(r.URL.Path, "/templates"):
			_ = json.NewEncoder(w).Encode(map[string]any{"template": []any{}})
		case strings.HasSuffix(r.URL.Path, "/folders"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"folder": []*tagmanager.Folder{{FolderId: "88", Name: "Google - GA4 Ecommerce"}},
			})
		case strings.HasSuffix(r.URL.Path, "/variables"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"variable": []*tagmanager.Variable{{VariableId: "99", Name: "CONST - GA4 Measurement ID"}},
			})
		case strings.HasSuffix(r.URL.Path, "/triggers"):
			_ = json.NewEncoder(w).Encode(map[string]any{"trigger": []any{}})
		case strings.HasSuffix(r.URL.Path, "/tags"):
			_ = json.NewEncoder(w).Encode(map[string]any{"tag": []any{}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := newMockBlueprintClient(t, srv, safety.DefaultPolicy())
	plan, err := Plan(context.Background(), client, reg, PlanInput{
		AccountID:   "1",
		ContainerID: "2",
		WorkspaceID: "3",
		BlueprintID: "google-ga4-ecommerce",
		Params:      map[string]string{"measurement_id": "G-TEST1234"},
	})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	if len(plan.MissingBuiltIns) != 0 {
		t.Fatalf("expected 0 missing built-ins, got %v", plan.MissingBuiltIns)
	}
	if len(plan.ProposedChange.Changes) != 2 { // only trigger + tag remain to create
		t.Fatalf("expected 2 changes (trigger + tag), got %d", len(plan.ProposedChange.Changes))
	}
	trig := plan.ProposedChange.Changes[0].Trigger
	tag := plan.ProposedChange.Changes[1].Tag
	if trig.ParentFolderId != "88" || tag.ParentFolderId != "88" {
		t.Fatalf("expected reused existing folder ID 88, got trig=%q tag=%q", trig.ParentFolderId, tag.ParentFolderId)
	}
	if len(tag.FiringTriggerId) != 1 || tag.FiringTriggerId[0] != trig.TriggerId {
		t.Fatalf("expected tag firingTriggerId=%q to match new trigger id=%q", tag.FiringTriggerId, trig.TriggerId)
	}
}

func TestApplyMetaPixelImportsTemplatesAndBulkUpdates(t *testing.T) {
	reg, err := NewDefaultRegistry()
	if err != nil {
		t.Fatalf("NewDefaultRegistry failed: %v", err)
	}

	var imported []*tagmanager.CustomTemplate
	var bulkCalls atomic.Int32
	var gotBulk *tagmanager.ProposedChange

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/workspaces"):
			_ = json.NewEncoder(w).Encode(map[string]any{"workspace": []any{}})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/workspaces"):
			_ = json.NewEncoder(w).Encode(&tagmanager.Workspace{WorkspaceId: "42", Name: "bp-meta-web"})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/built_in_variables"):
			_ = json.NewEncoder(w).Encode(map[string]any{"builtInVariable": []any{}})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/built_in_variables"):
			_ = json.NewEncoder(w).Encode(map[string]any{"builtInVariable": []*tagmanager.BuiltInVariable{{Type: "event"}, {Type: "pageUrl"}}})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/templates"):
			_ = json.NewEncoder(w).Encode(map[string]any{"template": imported})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/templates:import_from_gallery"):
			q := r.URL.Query()
			id := "50"
			if q.Get("galleryRepository") == "unique-event-id-variable" {
				id = "51"
			}
			tpl := &tagmanager.CustomTemplate{
				TemplateId: id,
				Name:       q.Get("galleryRepository"),
				GalleryReference: &tagmanager.GalleryReference{
					Owner:      q.Get("galleryOwner"),
					Repository: q.Get("galleryRepository"),
				},
			}
			imported = append(imported, tpl)
			_ = json.NewEncoder(w).Encode(tpl)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/folders"):
			_ = json.NewEncoder(w).Encode(map[string]any{"folder": []any{}})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/variables"):
			_ = json.NewEncoder(w).Encode(map[string]any{"variable": []any{}})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/triggers"):
			_ = json.NewEncoder(w).Encode(map[string]any{"trigger": []any{}})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/tags"):
			_ = json.NewEncoder(w).Encode(map[string]any{"tag": []any{}})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/bulk_update"):
			bulkCalls.Add(1)
			var body tagmanager.ProposedChange
			if decErr := json.NewDecoder(r.Body).Decode(&body); decErr != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			gotBulk = &body
			_ = json.NewEncoder(w).Encode(&tagmanager.BulkUpdateWorkspaceResponse{Changes: body.Changes})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	// read-only blocks Apply immediately.
	roClient := newMockBlueprintClient(t, srv, &safety.Policy{Mode: safety.ModeReadOnly})
	_, err = Apply(context.Background(), roClient, reg, ApplyInput{
		PlanInput: PlanInput{
			AccountID:   "1",
			ContainerID: "2",
			WorkspaceID: "3",
			BlueprintID: "meta-pixel-web",
			Params:      map[string]string{"pixel_id": "99887766"},
		},
		ImportGalleryTemplates: true,
	})
	if !errors.Is(err, safety.ErrBlocked) {
		t.Fatalf("expected ErrBlocked in read-only mode, got %v", err)
	}

	wsClient := newMockBlueprintClient(t, srv, safety.DefaultPolicy())
	res, err := Apply(context.Background(), wsClient, reg, ApplyInput{
		PlanInput: PlanInput{
			AccountID:   "1",
			ContainerID: "2",
			BlueprintID: "meta-pixel-web",
			Params:      map[string]string{"pixel_id": "99887766"},
		},
		CreateWorkspaceName:    "bp-meta-web",
		ImportGalleryTemplates: true,
	})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if !res.CreatedWorkspace || res.WorkspaceID != "42" {
		t.Fatalf("expected created workspace 42, got %+v", res)
	}
	if len(res.ImportedTemplates) != 2 || bulkCalls.Load() != 1 {
		t.Fatalf("expected 2 imported templates and 1 bulkUpdate call, got %+v bulkCalls=%d", res, bulkCalls.Load())
	}

	rawBulk, _ := json.Marshal(gotBulk)
	if !strings.Contains(string(rawBulk), `"type":"cvt_2_50"`) || !strings.Contains(string(rawBulk), `"type":"cvt_2_51"`) {
		t.Fatalf("expected resolved cvt_2_50 and cvt_2_51 in bulkUpdate payload: %s", string(rawBulk))
	}
}

func TestApplyShopifyAndTikTokAndAuditClean(t *testing.T) {
	reg, err := NewDefaultRegistry()
	if err != nil {
		t.Fatalf("NewDefaultRegistry failed: %v", err)
	}

	type wsState struct {
		templates []*tagmanager.CustomTemplate
		folders   []*tagmanager.Folder
		variables []*tagmanager.Variable
		triggers  []*tagmanager.Trigger
		tags      []*tagmanager.Tag
	}

	stateByWS := map[string]*wsState{
		"3": {},
		"4": {},
	}
	var nextTplID atomic.Int32
	nextTplID.Store(60)

	extractWS := func(path string) *wsState {
		parts := strings.Split(path, "/")
		for i := 0; i < len(parts)-1; i++ {
			if parts[i] == "workspaces" {
				st, ok := stateByWS[parts[i+1]]
				if !ok {
					st = &wsState{}
					stateByWS[parts[i+1]] = st
				}
				return st
			}
		}
		return &wsState{}
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		st := extractWS(r.URL.Path)
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/built_in_variables"):
			_ = json.NewEncoder(w).Encode(map[string]any{"builtInVariable": []any{}})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/built_in_variables"):
			_ = json.NewEncoder(w).Encode(map[string]any{"builtInVariable": []any{}})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/templates"):
			_ = json.NewEncoder(w).Encode(map[string]any{"template": st.templates})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/templates:import_from_gallery"):
			q := r.URL.Query()
			id := strings.TrimSpace(strings.TrimPrefix(strings.ReplaceAll(time.Unix(0, int64(nextTplID.Add(1))).String(), "", ""), ""))
			_ = id
			tplNum := nextTplID.Load()
			tpl := &tagmanager.CustomTemplate{
				TemplateId: string(rune('0'+tplNum/10)) + string(rune('0'+tplNum%10)),
				Name:       q.Get("galleryRepository"),
				GalleryReference: &tagmanager.GalleryReference{
					Owner:      q.Get("galleryOwner"),
					Repository: q.Get("galleryRepository"),
				},
			}
			st.templates = append(st.templates, tpl)
			_ = json.NewEncoder(w).Encode(tpl)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/folders"):
			_ = json.NewEncoder(w).Encode(map[string]any{"folder": st.folders})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/variables"):
			_ = json.NewEncoder(w).Encode(map[string]any{"variable": st.variables})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/triggers"):
			_ = json.NewEncoder(w).Encode(map[string]any{"trigger": st.triggers})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/tags"):
			_ = json.NewEncoder(w).Encode(map[string]any{"tag": st.tags})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/bulk_update"):
			var body tagmanager.ProposedChange
			if decErr := json.NewDecoder(r.Body).Decode(&body); decErr != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			for _, ch := range body.Changes {
				if ch.Folder != nil {
					st.folders = append(st.folders, ch.Folder)
				}
				if ch.Variable != nil {
					st.variables = append(st.variables, ch.Variable)
				}
				if ch.Trigger != nil {
					st.triggers = append(st.triggers, ch.Trigger)
				}
				if ch.Tag != nil {
					st.tags = append(st.tags, ch.Tag)
				}
			}
			_ = json.NewEncoder(w).Encode(&tagmanager.BulkUpdateWorkspaceResponse{Changes: body.Changes})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := newMockBlueprintClient(t, srv, safety.DefaultPolicy())
	ctx := context.Background()

	// 1. Minimal shopify-gtm-web on workspace 3.
	if _, err := Apply(ctx, client, reg, ApplyInput{
		PlanInput: PlanInput{
			AccountID:   "1",
			ContainerID: "2",
			WorkspaceID: "3",
			BlueprintID: "shopify-gtm-web",
			Params:      map[string]string{"measurement_id": "G-SHOPMIN1"},
		},
		ImportGalleryTemplates: true,
	}); err != nil {
		t.Fatalf("Apply minimal shopify-gtm-web failed: %v", err)
	}

	ws3 := stateByWS["3"]
	minReport := auditor.EvaluateSnapshot(auditor.Input{MinSeverity: auditor.SeverityLow}, &auditor.Snapshot{
		AccountID:   "1",
		ContainerID: "2",
		WorkspaceID: "3",
		TargetLabel: "workspace 3",
		Templates:   ws3.templates,
		Folders:     ws3.folders,
		Variables:   ws3.variables,
		Triggers:    ws3.triggers,
		Tags:        ws3.tags,
	})
	if auditor.HasFindingsAtOrAbove(minReport.Findings, auditor.SeverityHigh) {
		t.Fatalf("minimal shopify-gtm-web produced critical/high findings: %+v", minReport.Findings)
	}
	for _, f := range minReport.Findings {
		if f.RuleID == "UNUSED_VARIABLE" || f.RuleID == "UNUSED_TRIGGER" {
			t.Fatalf("minimal shopify-gtm-web left orphan entity: %+v", f)
		}
	}

	// 2. Full shopify-gtm-web + tiktok-pixel-web composed on workspace 4.
	if _, err := Apply(ctx, client, reg, ApplyInput{
		PlanInput: PlanInput{
			AccountID:   "1",
			ContainerID: "2",
			WorkspaceID: "4",
			BlueprintID: "shopify-gtm-web",
			Params: map[string]string{
				"measurement_id":            "G-SHOPFULL2",
				"server_container_url":      "https://metrics.store.com",
				"google_ads_conversion_id":  "123456789",
				"google_ads_purchase_label": "AbCdEfGh",
			},
		},
		ImportGalleryTemplates: true,
	}); err != nil {
		t.Fatalf("Apply full shopify-gtm-web failed: %v", err)
	}

	ttRes, err := Apply(ctx, client, reg, ApplyInput{
		PlanInput: PlanInput{
			AccountID:   "1",
			ContainerID: "2",
			WorkspaceID: "4",
			BlueprintID: "tiktok-pixel-web",
			Params:      map[string]string{"pixel_id": "CD9079RC77U0N3GBV16Y"},
		},
		ImportGalleryTemplates: true,
	})
	if err != nil {
		t.Fatalf("Apply tiktok-pixel-web after shopify-gtm-web failed: %v", err)
	}
	if ttRes.SkippedExisting == 0 {
		t.Fatalf("expected tiktok-pixel-web to reuse VAR - Unique Event ID from shopify-gtm-web")
	}

	ws4 := stateByWS["4"]
	fullReport := auditor.EvaluateSnapshot(auditor.Input{MinSeverity: auditor.SeverityLow}, &auditor.Snapshot{
		AccountID:   "1",
		ContainerID: "2",
		WorkspaceID: "4",
		TargetLabel: "workspace 4",
		Templates:   ws4.templates,
		Folders:     ws4.folders,
		Variables:   ws4.variables,
		Triggers:    ws4.triggers,
		Tags:        ws4.tags,
	})
	if auditor.HasFindingsAtOrAbove(fullReport.Findings, auditor.SeverityHigh) {
		t.Fatalf("composed shopify-gtm-web + tiktok-pixel-web produced critical/high findings: %+v", fullReport.Findings)
	}
	for _, f := range fullReport.Findings {
		if f.RuleID == "UNUSED_VARIABLE" || f.RuleID == "UNUSED_TRIGGER" || f.RuleID == "PLATFORM_DEDUP_MISSING_EVENT_ID" {
			t.Fatalf("composed workspace left unexpected finding: %+v", f)
		}
	}
}
