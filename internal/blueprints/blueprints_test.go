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
	if len(specs) != 7 {
		t.Fatalf("expected 7 embedded blueprints, got %d", len(specs))
	}

	expectedIDs := []string{
		"consent-mode-v2",
		"google-ads-conversions",
		"google-ga4-ecommerce",
		"google-ga4-leadgen",
		"google-tag-ga4-base",
		"meta-capi-sgtm",
		"meta-pixel-web",
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

func TestPlanAllSevenBlueprintsAndRedactSecrets(t *testing.T) {
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
			if tc.id == "meta-capi-sgtm" {
				if strings.Contains(s, "EAAB_SECRET_TOKEN_XYZ") {
					t.Fatalf("access_token leaked into public PlanResult JSON!")
				}
				if plan.ResolvedParams["access_token"] != "[REDACTED]" {
					t.Fatalf("expected [REDACTED] in ResolvedParams, got %q", plan.ResolvedParams["access_token"])
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
