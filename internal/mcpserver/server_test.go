package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LorranHippolyte/tagforge/internal/blueprints"
	"github.com/LorranHippolyte/tagforge/internal/gtm"
	"github.com/LorranHippolyte/tagforge/internal/safety"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/api/option"
	"google.golang.org/api/tagmanager/v2"
)

func connectInMemory(t *testing.T, srv *mcp.Server) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()

	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatalf("server.Connect failed: %v", err)
	}
	t.Cleanup(func() { _ = ss.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.1.0"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client.Connect failed: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func newTestClient(t *testing.T, srv *httptest.Server, policy *safety.Policy) *gtm.Client {
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
		t.Fatalf("gtm.NewClient failed: %v", err)
	}
	return c
}

func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		return ""
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected *mcp.TextContent, got %T", res.Content[0])
	}
	return tc.Text
}

func TestMCPServerListToolsAndExecuteFlow(t *testing.T) {
	var publishHits, bulkHits atomic.Int32
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/tags"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tag": []*tagmanager.Tag{{TagId: "10", Name: "GA4 - Config", Type: "googtag", Fingerprint: "fp-10"}},
			})
		case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/tags/10"):
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/versions/5:publish"):
			publishHits.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"containerVersion": &tagmanager.ContainerVersion{ContainerVersionId: "5", Name: "v5"},
			})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/built_in_variables"):
			_ = json.NewEncoder(w).Encode(map[string]any{"builtInVariable": []any{}})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/built_in_variables"):
			_ = json.NewEncoder(w).Encode(map[string]any{"builtInVariable": []*tagmanager.BuiltInVariable{{Type: "event"}, {Type: "pageUrl"}}})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/templates"):
			_ = json.NewEncoder(w).Encode(map[string]any{"template": []any{}})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/folders"):
			_ = json.NewEncoder(w).Encode(map[string]any{"folder": []any{}})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/variables"):
			_ = json.NewEncoder(w).Encode(map[string]any{"variable": []any{}})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/triggers"):
			_ = json.NewEncoder(w).Encode(map[string]any{"trigger": []any{}})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/bulk_update"):
			bulkHits.Add(1)
			var body tagmanager.ProposedChange
			_ = json.NewDecoder(r.Body).Decode(&body)
			_ = json.NewEncoder(w).Encode(&tagmanager.BulkUpdateWorkspaceResponse{Changes: body.Changes})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer httpSrv.Close()

	reg, err := blueprints.NewDefaultRegistry()
	if err != nil {
		t.Fatalf("NewDefaultRegistry failed: %v", err)
	}

	wsSrv, err := NewServer(Config{
		Client:   newTestClient(t, httpSrv, safety.DefaultPolicy()),
		Registry: reg,
		Version:  "0.2.0-test",
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	cs := connectInMemory(t, wsSrv)
	ctx := context.Background()

	// 1. Verify exactly 17 tools and short names <= 24 chars.
	toolsRes, err := cs.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	if len(toolsRes.Tools) != 17 {
		t.Fatalf("expected 17 tools, got %d", len(toolsRes.Tools))
	}
	for _, tool := range toolsRes.Tools {
		if len(tool.Name) > 24 {
			t.Fatalf("tool name %q exceeds 24 chars (%d)", tool.Name, len(tool.Name))
		}
	}

	// 2. Call gtm_guide.
	guideRes, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "gtm_guide",
		Arguments: map[string]any{"topic": "meta_capi_dedup"},
	})
	if err != nil || guideRes.IsError {
		t.Fatalf("gtm_guide failed: err=%v isErr=%v", err, guideRes.IsError)
	}
	if !strings.Contains(resultText(t, guideRes), "event_id") {
		t.Fatalf("unexpected gtm_guide content")
	}

	// 3. Call gtm_blueprint_list -> 7 blueprints.
	bpListRes, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "gtm_blueprint_list"})
	if err != nil || bpListRes.IsError {
		t.Fatalf("gtm_blueprint_list failed: %v", err)
	}
	if !strings.Contains(resultText(t, bpListRes), "google-ga4-ecommerce") {
		t.Fatalf("expected google-ga4-ecommerce in blueprint list")
	}

	// 4. Plan and Apply google-ga4-ecommerce via MCP.
	planRes, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "gtm_blueprint_plan",
		Arguments: map[string]any{
			"accountId":   "1",
			"containerId": "2",
			"workspaceId": "3",
			"blueprintId": "google-ga4-ecommerce",
			"params":      map[string]string{"measurement_id": "G-TEST1234"},
		},
	})
	if err != nil || planRes.IsError {
		t.Fatalf("gtm_blueprint_plan failed: err=%v text=%s", err, resultText(t, planRes))
	}

	applyRes, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "gtm_blueprint_apply",
		Arguments: map[string]any{
			"accountId":   "1",
			"containerId": "2",
			"workspaceId": "3",
			"blueprintId": "google-ga4-ecommerce",
			"params":      map[string]string{"measurement_id": "G-TEST1234"},
		},
	})
	if err != nil || applyRes.IsError {
		t.Fatalf("gtm_blueprint_apply failed: err=%v text=%s", err, resultText(t, applyRes))
	}
	if bulkHits.Load() != 1 {
		t.Fatalf("expected 1 bulkUpdate hit from gtm_blueprint_apply, got %d", bulkHits.Load())
	}

	// 5. gtm_tag list & delete confirmation gate.
	listRes, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "gtm_tag",
		Arguments: map[string]any{
			"action":      "list",
			"accountId":   "1",
			"containerId": "2",
			"workspaceId": "3",
		},
	})
	if err != nil || listRes.IsError || !strings.Contains(resultText(t, listRes), "GA4 - Config") {
		t.Fatalf("gtm_tag list failed: err=%v text=%s", err, resultText(t, listRes))
	}

	delNoConfirm, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "gtm_tag",
		Arguments: map[string]any{
			"action":      "delete",
			"accountId":   "1",
			"containerId": "2",
			"workspaceId": "3",
			"tagId":       "10",
		},
	})
	if err != nil || !delNoConfirm.IsError || !strings.Contains(resultText(t, delNoConfirm), "--confirm") {
		t.Fatalf("expected IsError mentioning confirm for gtm_tag delete without confirm, got err=%v isErr=%v text=%s", err, delNoConfirm.IsError, resultText(t, delNoConfirm))
	}

	delConfirmed, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "gtm_tag",
		Arguments: map[string]any{
			"action":      "delete",
			"accountId":   "1",
			"containerId": "2",
			"workspaceId": "3",
			"tagId":       "10",
			"confirm":     true,
		},
	})
	if err != nil || delConfirmed.IsError {
		t.Fatalf("gtm_tag delete with confirm failed: err=%v text=%s", err, resultText(t, delConfirmed))
	}

	// 6. gtm_version publish blocked in workspace-only without hitting server.
	pubBlocked, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "gtm_version",
		Arguments: map[string]any{
			"action":      "publish",
			"accountId":   "1",
			"containerId": "2",
			"versionId":   "5",
			"fingerprint": "fp-5",
			"confirm":     true,
		},
	})
	if err != nil || !pubBlocked.IsError || publishHits.Load() != 0 {
		t.Fatalf("expected publish blocked in workspace-only; err=%v isErr=%v hits=%d", err, pubBlocked.IsError, publishHits.Load())
	}

	// 7. Full-mode MCP server allows publish with confirm=true.
	fullSrv, err := NewServer(Config{
		Client:   newTestClient(t, httpSrv, &safety.Policy{Mode: safety.ModeFull}),
		Registry: reg,
		Version:  "0.2.0-test",
	})
	if err != nil {
		t.Fatalf("NewServer full failed: %v", err)
	}
	fullCS := connectInMemory(t, fullSrv)
	pubOK, err := fullCS.CallTool(ctx, &mcp.CallToolParams{
		Name: "gtm_version",
		Arguments: map[string]any{
			"action":      "publish",
			"accountId":   "1",
			"containerId": "2",
			"versionId":   "5",
			"fingerprint": "fp-5",
			"confirm":     true,
		},
	})
	if err != nil || pubOK.IsError || publishHits.Load() != 1 {
		t.Fatalf("expected publish success in full mode; err=%v isErr=%v hits=%d text=%s", err, pubOK.IsError, publishHits.Load(), resultText(t, pubOK))
	}
}
