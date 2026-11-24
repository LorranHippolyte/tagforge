package gtm

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

	"github.com/LorranHippolyte/tagforge/internal/safety"
	"google.golang.org/api/option"
	"google.golang.org/api/tagmanager/v2"
)

func newTestClient(t *testing.T, srv *httptest.Server, policy *safety.Policy) *Client {
	t.Helper()
	c, err := NewClient(context.Background(), ClientConfig{
		ClientOptions: []option.ClientOption{
			option.WithHTTPClient(srv.Client()),
			option.WithEndpoint(srv.URL),
			option.WithoutAuthentication(),
		},
		Policy: policy,
		Scheduler: NewScheduler(SchedulerOptions{
			QPS:         100,
			Burst:       20,
			MaxAttempts: 2,
			BaseBackoff: 2 * time.Millisecond,
		}),
		Cache: NewSnapshotCache(true, 30*time.Second),
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	return c
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func TestListTagsCachingAndMutationInvalidation(t *testing.T) {
	var listCalls atomic.Int32
	storedTags := []*tagmanager.Tag{{TagId: "10", Name: "GA4 - Config", Type: "googtag"}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/tagmanager/v2/accounts/1/containers/2/workspaces/3/tags":
			listCalls.Add(1)
			writeJSON(w, http.StatusOK, map[string]any{"tag": storedTags})
		case r.Method == http.MethodPost && r.URL.Path == "/tagmanager/v2/accounts/1/containers/2/workspaces/3/tags":
			var body tagmanager.Tag
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			body.TagId = "11"
			body.Fingerprint = "fp-11"
			storedTags = append(storedTags, &body)
			writeJSON(w, http.StatusOK, body)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := newTestClient(t, srv, safety.DefaultPolicy())
	ctx := context.Background()

	// 1st list hits API.
	tags, err := client.ListTags(ctx, "1", "2", "3", ListOptions{})
	if err != nil {
		t.Fatalf("ListTags #1 failed: %v", err)
	}
	if len(tags) != 1 || listCalls.Load() != 1 {
		t.Fatalf("unexpected initial tags=%d calls=%d", len(tags), listCalls.Load())
	}

	// 2nd list uses cache.
	tags, err = client.ListTags(ctx, "1", "2", "3", ListOptions{})
	if err != nil {
		t.Fatalf("ListTags #2 failed: %v", err)
	}
	if listCalls.Load() != 1 || len(tags) != 1 {
		t.Fatalf("expected cache hit; calls=%d", listCalls.Load())
	}

	// CreateTag runs fresh list (calls=2) for duplicate guard, creates tag, invalidates cache.
	created, err := client.CreateTag(ctx, "1", "2", "3", &tagmanager.Tag{Name: "GA4 - Purchase", Type: "gaawe"}, CreateOptions{})
	if err != nil {
		t.Fatalf("CreateTag failed: %v", err)
	}
	if created.TagId != "11" {
		t.Fatalf("unexpected created tag: %+v", created)
	}

	// Subsequent list hits API again because cache was invalidated.
	tags, err = client.ListTags(ctx, "1", "2", "3", ListOptions{})
	if err != nil {
		t.Fatalf("ListTags #3 failed: %v", err)
	}
	if listCalls.Load() != 3 || len(tags) != 2 {
		t.Fatalf("expected cache invalidation after write; calls=%d len=%d", listCalls.Load(), len(tags))
	}
}

func TestSafetyGatesBeforeNetworkAndDuplicateGuard(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/tagmanager/v2/accounts/1/containers/2/workspaces/3/tags":
			writeJSON(w, http.StatusOK, map[string]any{
				"tag": []*tagmanager.Tag{{TagId: "10", Name: "GA4 - Config", Type: "googtag"}},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/tagmanager/v2/accounts/1/containers/2/versions/5:publish":
			writeJSON(w, http.StatusOK, map[string]any{
				"containerVersion": &tagmanager.ContainerVersion{ContainerVersionId: "5", Name: "Release"},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	ctx := context.Background()
	wsClient := newTestClient(t, srv, &safety.Policy{Mode: safety.ModeWorkspaceOnly})

	// Publish must fail locally without hitting server.
	_, err := wsClient.PublishVersion(ctx, "1", "2", "5", "fp-5", true)
	if !errors.Is(err, safety.ErrBlocked) {
		t.Fatalf("expected ErrBlocked for publish in workspace-only, got %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("safety block should not make HTTP calls; hits=%d", hits.Load())
	}

	// Delete without confirm must fail locally.
	err = wsClient.DeleteTag(ctx, "1", "2", "3", "10", false)
	if !errors.Is(err, safety.ErrBlocked) {
		t.Fatalf("expected ErrBlocked for delete without confirm, got %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("confirm gate should not make HTTP calls; hits=%d", hits.Load())
	}

	// Disable built-in variable without confirm must fail locally.
	err = wsClient.DisableBuiltInVariables(ctx, "1", "2", "3", []string{"pageUrl"}, false)
	if !errors.Is(err, safety.ErrBlocked) {
		t.Fatalf("expected ErrBlocked for disable builtin without confirm, got %v", err)
	}

	// Update without fingerprint must fail validation locally.
	_, err = wsClient.UpdateTag(ctx, "1", "2", "3", "10", "", &tagmanager.Tag{Name: "X"})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation without fingerprint, got %v", err)
	}

	// Duplicate name guard prevents duplicate tag creation.
	_, err = wsClient.CreateTag(ctx, "1", "2", "3", &tagmanager.Tag{Name: "ga4 - config"}, CreateOptions{})
	if !errors.Is(err, safety.ErrDuplicate) {
		t.Fatalf("expected ErrDuplicate, got %v", err)
	}

	// Full mode + confirm + fingerprint succeeds.
	fullClient := newTestClient(t, srv, &safety.Policy{Mode: safety.ModeFull})
	if _, err := fullClient.PublishVersion(ctx, "1", "2", "5", "fp-5", true); err != nil {
		t.Fatalf("expected PublishVersion in full mode with confirm to succeed, got %v", err)
	}
}

func TestCreateVersionChecksConflictsAndEmptyWorkspace(t *testing.T) {
	scenario := "conflicts"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/tagmanager/v2/accounts/1/containers/2/workspaces/3/status":
			switch scenario {
			case "conflicts":
				writeJSON(w, http.StatusOK, map[string]any{
					"mergeConflict":   []map[string]any{{"changeStatus": "added"}},
					"workspaceChange": []map[string]any{{"changeStatus": "added"}},
				})
			case "empty":
				writeJSON(w, http.StatusOK, map[string]any{
					"mergeConflict":   []any{},
					"workspaceChange": []any{},
				})
			case "ready":
				writeJSON(w, http.StatusOK, map[string]any{
					"workspaceChange": []map[string]any{{"changeStatus": "added"}},
				})
			}
		case r.Method == http.MethodPost && r.URL.Path == "/tagmanager/v2/accounts/1/containers/2/workspaces/3:create_version":
			writeJSON(w, http.StatusOK, map[string]any{
				"containerVersion": &tagmanager.ContainerVersion{ContainerVersionId: "9", Name: "v9", Fingerprint: "fp-9"},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := newTestClient(t, srv, safety.DefaultPolicy())
	ctx := context.Background()

	_, err := client.CreateVersion(ctx, "1", "2", "3", CreateVersionOptions{Name: "v9"})
	if !errors.Is(err, ErrWorkspaceHasConflicts) {
		t.Fatalf("expected ErrWorkspaceHasConflicts, got %v", err)
	}

	scenario = "empty"
	_, err = client.CreateVersion(ctx, "1", "2", "3", CreateVersionOptions{Name: "v9"})
	if !errors.Is(err, ErrWorkspaceNoChanges) {
		t.Fatalf("expected ErrWorkspaceNoChanges, got %v", err)
	}

	scenario = "ready"
	res, err := client.CreateVersion(ctx, "1", "2", "3", CreateVersionOptions{Name: "v9", Notes: "Ready"})
	if err != nil {
		t.Fatalf("CreateVersion failed: %v", err)
	}
	if res.ContainerVersion == nil || res.ContainerVersion.ContainerVersionId != "9" {
		t.Fatalf("unexpected CreateVersion response: %+v", res)
	}
}

func TestAdditionalEndpointsAndErrorTranslation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/tagmanager/v2/accounts/containers:lookup":
			writeJSON(w, http.StatusOK, map[string]any{"containerId": "2", "publicId": "GTM-TEST123"})
		case r.Method == http.MethodGet && r.URL.Path == "/tagmanager/v2/accounts/1/containers/2:snippet":
			writeJSON(w, http.StatusOK, map[string]any{"snippet": "<script>gtm</script>"})
		case r.Method == http.MethodGet && r.URL.Path == "/tagmanager/v2/accounts/1/containers/2/workspaces/3/templates":
			writeJSON(w, http.StatusOK, map[string]any{"template": []*tagmanager.CustomTemplate{{TemplateId: "t1", Name: "Meta Pixel"}}})
		case r.Method == http.MethodGet && r.URL.Path == "/tagmanager/v2/accounts/1/containers/2/workspaces/3/clients":
			writeJSON(w, http.StatusOK, map[string]any{"client": []*tagmanager.Client{{ClientId: "c1", Name: "GA4 Client", Type: "ga4"}}})
		case r.Method == http.MethodGet && r.URL.Path == "/tagmanager/v2/accounts/1/containers/2/workspaces/3/transformations":
			writeJSON(w, http.StatusOK, map[string]any{"transformation": []*tagmanager.Transformation{{TransformationId: "tr1", Name: "Exclude PII"}}})
		case r.Method == http.MethodPost && r.URL.Path == "/tagmanager/v2/accounts/1/containers/2/workspaces/3/folders/f1:entities":
			writeJSON(w, http.StatusOK, map[string]any{"tag": []*tagmanager.Tag{{TagId: "10", Name: "Tag inside folder"}}})
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/tagmanager/v2/accounts/1/containers/2/workspaces/3/tags/999"):
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": map[string]any{"code": 409, "message": "Fingerprint mismatch"},
			})
		default:
			writeJSON(w, http.StatusNotFound, map[string]any{
				"error": map[string]any{"code": 404, "message": "not found"},
			})
		}
	}))
	defer srv.Close()

	client := newTestClient(t, srv, safety.DefaultPolicy())
	ctx := context.Background()

	c, err := client.LookupContainer(ctx, "GTM-TEST123")
	if err != nil || c.ContainerId != "2" {
		t.Fatalf("LookupContainer failed: %v %+v", err, c)
	}
	snip, err := client.GetContainerSnippet(ctx, "1", "2")
	if err != nil || !strings.Contains(snip.Snippet, "<script>") {
		t.Fatalf("GetContainerSnippet failed: %v %+v", err, snip)
	}
	tpls, err := client.ListTemplates(ctx, "1", "2", "3", ListOptions{})
	if err != nil || len(tpls) != 1 || tpls[0].TemplateId != "t1" {
		t.Fatalf("ListTemplates failed: %v %+v", err, tpls)
	}
	clients, err := client.ListClients(ctx, "1", "2", "3", ListOptions{})
	if err != nil || len(clients) != 1 || clients[0].ClientId != "c1" {
		t.Fatalf("ListClients failed: %v %+v", err, clients)
	}
	trs, err := client.ListTransformations(ctx, "1", "2", "3", ListOptions{})
	if err != nil || len(trs) != 1 || trs[0].TransformationId != "tr1" {
		t.Fatalf("ListTransformations failed: %v %+v", err, trs)
	}
	fe, err := client.ListFolderEntities(ctx, "1", "2", "3", "f1")
	if err != nil || len(fe.Tag) != 1 {
		t.Fatalf("ListFolderEntities failed: %v %+v", err, fe)
	}

	_, err = client.UpdateTag(ctx, "1", "2", "3", "999", "stale-fp", &tagmanager.Tag{Name: "X"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict on 409, got %v", err)
	}

	_, err = client.GetTag(ctx, "1", "2", "3", "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound on 404, got %v", err)
	}
}

func TestImportTemplateFromGallery(t *testing.T) {
	var listCalls atomic.Int32
	var gotOwner, gotRepo, gotAck, gotSHA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/tagmanager/v2/accounts/1/containers/2/workspaces/3/templates":
			listCalls.Add(1)
			writeJSON(w, http.StatusOK, map[string]any{"template": []*tagmanager.CustomTemplate{}})
		case r.Method == http.MethodPost && r.URL.Path == "/tagmanager/v2/accounts/1/containers/2/workspaces/3/templates:import_from_gallery":
			q := r.URL.Query()
			gotOwner = q.Get("galleryOwner")
			gotRepo = q.Get("galleryRepository")
			gotAck = q.Get("acknowledgePermissions")
			gotSHA = q.Get("gallerySha")
			writeJSON(w, http.StatusOK, map[string]any{
				"templateId": "77",
				"name":       "Facebook Pixel by Stape",
				"galleryReference": map[string]any{
					"owner":      gotOwner,
					"repository": gotRepo,
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	// read-only must block ImportTemplateFromGallery before touching network.
	roClient := newTestClient(t, srv, &safety.Policy{Mode: safety.ModeReadOnly})
	_, err := roClient.ImportTemplateFromGallery(context.Background(), "1", "2", "3", ImportGalleryTemplateOptions{
		Owner:      "stape-io",
		Repository: "fb-tag",
	})
	if !errors.Is(err, safety.ErrBlocked) {
		t.Fatalf("expected ErrBlocked in read-only mode, got %v", err)
	}

	wsClient := newTestClient(t, srv, safety.DefaultPolicy())
	// Prime template list cache.
	if _, err := wsClient.ListTemplates(context.Background(), "1", "2", "3", ListOptions{}); err != nil {
		t.Fatalf("ListTemplates failed: %v", err)
	}

	tpl, err := wsClient.ImportTemplateFromGallery(context.Background(), "1", "2", "3", ImportGalleryTemplateOptions{
		Owner:                  "stape-io",
		Repository:             "fb-tag",
		SHA:                    "abc123",
		AcknowledgePermissions: true,
	})
	if err != nil {
		t.Fatalf("ImportTemplateFromGallery failed: %v", err)
	}
	if tpl.TemplateId != "77" || gotOwner != "stape-io" || gotRepo != "fb-tag" || gotAck != "true" || gotSHA != "abc123" {
		t.Fatalf("unexpected import result: tpl=%+v owner=%s repo=%s ack=%s sha=%s", tpl, gotOwner, gotRepo, gotAck, gotSHA)
	}

	// Cache must be invalidated after import.
	if _, err := wsClient.ListTemplates(context.Background(), "1", "2", "3", ListOptions{}); err != nil {
		t.Fatalf("ListTemplates after import failed: %v", err)
	}
	if listCalls.Load() != 2 {
		t.Fatalf("expected template cache invalidation after import; listCalls=%d", listCalls.Load())
	}
}
