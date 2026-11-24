package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LorranHippolyte/tagforge/internal/blueprints"
	"github.com/LorranHippolyte/tagforge/internal/safety"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/api/tagmanager/v2"
)

type bearerRoundTripper struct {
	token string
	base  http.RoundTripper
}

func (b *bearerRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	req := r.Clone(r.Context())
	req.Header.Set("Authorization", "Bearer "+b.token)
	rt := b.base
	if rt == nil {
		rt = http.DefaultTransport
	}
	return rt.RoundTrip(req)
}

func TestHTTPConfigSecurityValidation(t *testing.T) {
	cases := []struct {
		name    string
		cfg     HTTPConfig
		wantErr string
	}{
		{
			name:    "non-loopback without auth token is refused",
			cfg:     HTTPConfig{Addr: "0.0.0.0:8080"},
			wantErr: "--auth-token",
		},
		{
			name: "non-loopback with auth token is allowed",
			cfg:  HTTPConfig{Addr: "0.0.0.0:8080", AuthToken: "s3cr3t"},
		},
		{
			name: "non-loopback with AllowInsecureNoAuth is allowed",
			cfg:  HTTPConfig{Addr: "0.0.0.0:8080", AllowInsecureNoAuth: true},
		},
		{
			name: "loopback ipv4 without token is allowed",
			cfg:  HTTPConfig{Addr: "127.0.0.1:8080"},
		},
		{
			name: "loopback ipv6 without token is allowed",
			cfg:  HTTPConfig{Addr: "[::1]:8080"},
		},
		{
			name:    "invalid addr fails",
			cfg:     HTTPConfig{Addr: "not-a-host-port"},
			wantErr: "invalid HTTP listen address",
		},
		{
			name:    "path without leading slash fails",
			cfg:     HTTPConfig{Addr: "127.0.0.1:8080", Path: "mcp"},
			wantErr: "must start with '/'",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg
			err := ValidateHTTPConfig(&cfg)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestHTTPHandlerHealthzAuthAndStreamableMCP(t *testing.T) {
	var publishHits atomic.Int32
	gtmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, ":publish") {
			publishHits.Add(1)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"account": []*tagmanager.Account{{AccountId: "1", Name: "Genesis Work"}},
		})
	}))
	defer gtmSrv.Close()

	reg, err := blueprints.NewDefaultRegistry()
	if err != nil {
		t.Fatalf("NewDefaultRegistry failed: %v", err)
	}

	handler, err := NewHTTPHandler(HTTPConfig{
		Config: Config{
			Client:   newTestClient(t, gtmSrv, safety.DefaultPolicy()),
			Policy:   safety.DefaultPolicy(),
			Registry: reg,
			Version:  "0.5.0-test",
		},
		Addr:      "127.0.0.1:8080",
		AuthToken: "secret-bearer-token",
	})
	if err != nil {
		t.Fatalf("NewHTTPHandler failed: %v", err)
	}

	ts := httptest.NewServer(handler)
	defer ts.Close()

	// 1. /healthz works without Bearer auth.
	resp, err := ts.Client().Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected /healthz 200, got %d", resp.StatusCode)
	}
	var health map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatalf("decode /healthz failed: %v", err)
	}
	if health["status"] != "ok" || health["mode"] != "workspace-only" {
		t.Fatalf("unexpected /healthz body: %+v", health)
	}

	// 2. /mcp rejects missing and invalid Bearer tokens with 401 + WWW-Authenticate.
	unauthResp, err := ts.Client().Post(ts.URL+"/mcp", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("unauth POST /mcp failed: %v", err)
	}
	unauthResp.Body.Close()
	if unauthResp.StatusCode != http.StatusUnauthorized || !strings.Contains(unauthResp.Header.Get("WWW-Authenticate"), "Bearer") {
		t.Fatalf("expected 401 with Bearer challenge, got status=%d www-auth=%q", unauthResp.StatusCode, unauthResp.Header.Get("WWW-Authenticate"))
	}

	badReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(`{}`))
	badReq.Header.Set("Authorization", "Bearer wrong-token")
	badReq.Header.Set("Content-Type", "application/json")
	badResp, err := ts.Client().Do(badReq)
	if err != nil {
		t.Fatalf("bad bearer POST /mcp failed: %v", err)
	}
	badResp.Body.Close()
	if badResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 on wrong token, got %d", badResp.StatusCode)
	}

	// 3. Connect real MCP Streamable HTTP client with valid Bearer token.
	ctx := context.Background()
	authHTTPClient := &http.Client{
		Transport: &bearerRoundTripper{
			token: "secret-bearer-token",
			base:  ts.Client().Transport,
		},
	}
	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "http-test-client", Version: "0.1.0"}, nil)
	cs, err := mcpClient.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   ts.URL + "/mcp",
		HTTPClient: authHTTPClient,
	}, nil)
	if err != nil {
		t.Fatalf("StreamableClientTransport Connect failed: %v", err)
	}
	defer cs.Close()

	toolsRes, err := cs.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools over HTTP failed: %v", err)
	}
	if len(toolsRes.Tools) != 19 {
		t.Fatalf("expected 19 tools over HTTP, got %d", len(toolsRes.Tools))
	}

	bpRes, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "gtm_blueprint_list"})
	if err != nil || bpRes.IsError || !strings.Contains(resultText(t, bpRes), "shopify-gtm-web") {
		t.Fatalf("gtm_blueprint_list over HTTP failed: err=%v isErr=%v text=%s", err, bpRes.IsError, resultText(t, bpRes))
	}

	// Verify workspace-only policy blocks publish even over HTTP.
	pubRes, err := cs.CallTool(ctx, &mcp.CallToolParams{
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
	if err != nil || !pubRes.IsError || publishHits.Load() != 0 {
		t.Fatalf("expected publish blocked over HTTP; err=%v isErr=%v hits=%d", err, pubRes.IsError, publishHits.Load())
	}

	promptsRes, err := cs.ListPrompts(ctx, &mcp.ListPromptsParams{})
	if err != nil || len(promptsRes.Prompts) != 7 {
		t.Fatalf("expected 7 prompts over HTTP, got err=%v count=%d", err, len(promptsRes.Prompts))
	}

	resRead, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "gtm://guides/shopify_custom_pixel"})
	if err != nil || len(resRead.Contents) == 0 || !strings.Contains(resRead.Contents[0].Text, "customerPrivacy") {
		t.Fatalf("ReadResource over HTTP failed: err=%v contents=%+v", err, resRead)
	}
}

func TestHTTPHandlerCORSAllowlist(t *testing.T) {
	gtmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{})
	}))
	defer gtmSrv.Close()

	reg, err := blueprints.NewDefaultRegistry()
	if err != nil {
		t.Fatalf("NewDefaultRegistry failed: %v", err)
	}

	handler, err := NewHTTPHandler(HTTPConfig{
		Config: Config{
			Client:   newTestClient(t, gtmSrv, safety.DefaultPolicy()),
			Policy:   safety.DefaultPolicy(),
			Registry: reg,
		},
		Addr:           "127.0.0.1:8080",
		AllowedOrigins: []string{"https://app.example.com"},
	})
	if err != nil {
		t.Fatalf("NewHTTPHandler failed: %v", err)
	}

	ts := httptest.NewServer(handler)
	defer ts.Close()

	// Allowed origin preflight -> 204 with Access-Control-Allow-Origin.
	allowReq, _ := http.NewRequest(http.MethodOptions, ts.URL+"/mcp", nil)
	allowReq.Header.Set("Origin", "https://app.example.com")
	allowResp, err := ts.Client().Do(allowReq)
	if err != nil {
		t.Fatalf("allowed OPTIONS failed: %v", err)
	}
	allowResp.Body.Close()
	if allowResp.StatusCode != http.StatusNoContent || allowResp.Header.Get("Access-Control-Allow-Origin") != "https://app.example.com" {
		t.Fatalf("expected 204 with allow-origin, got status=%d header=%q", allowResp.StatusCode, allowResp.Header.Get("Access-Control-Allow-Origin"))
	}

	// Disallowed origin preflight -> 403 with no Access-Control-Allow-Origin.
	denyReq, _ := http.NewRequest(http.MethodOptions, ts.URL+"/mcp", nil)
	denyReq.Header.Set("Origin", "https://evil.example.com")
	denyResp, err := ts.Client().Do(denyReq)
	if err != nil {
		t.Fatalf("disallowed OPTIONS failed: %v", err)
	}
	denyResp.Body.Close()
	if denyResp.StatusCode != http.StatusForbidden || denyResp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("expected 403 with no allow-origin, got status=%d header=%q", denyResp.StatusCode, denyResp.Header.Get("Access-Control-Allow-Origin"))
	}
}
