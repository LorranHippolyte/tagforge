package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LorranHippolyte/tagforge/internal/auth"
	"github.com/LorranHippolyte/tagforge/internal/gtm"
	"github.com/LorranHippolyte/tagforge/internal/safety"
	"github.com/zalando/go-keyring"
	"google.golang.org/api/option"
	"google.golang.org/api/tagmanager/v2"
)

func execCLI(t *testing.T, deps Dependencies, stdin string, args ...string) (string, string, error) {
	t.Helper()
	cmd := NewRootCmd(deps)
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	if stdin != "" {
		cmd.SetIn(strings.NewReader(stdin))
	}
	cmd.SetArgs(args)
	err := cmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

func mockCLIDeps(t *testing.T, srv *httptest.Server) Dependencies {
	t.Helper()
	return Dependencies{
		Getenv: func(string) string { return "" },
		ClientFactory: func(ctx context.Context, opts GlobalOptions, _ io.Writer) (*gtm.Client, error) {
			mode, err := safety.ParseMode(opts.Mode)
			if err != nil {
				return nil, err
			}
			return gtm.NewClient(ctx, gtm.ClientConfig{
				ClientOptions: []option.ClientOption{
					option.WithHTTPClient(srv.Client()),
					option.WithEndpoint(srv.URL),
					option.WithoutAuthentication(),
				},
				Policy: &safety.Policy{
					Mode:                 mode,
					AllowUserPermissions: opts.AllowUserPermissions,
				},
				Scheduler: gtm.NewScheduler(gtm.SchedulerOptions{
					QPS:         100,
					Burst:       20,
					MaxAttempts: 2,
					BaseBackoff: 2 * time.Millisecond,
				}),
				Cache: gtm.NewSnapshotCache(!opts.NoCache, 30*time.Second),
			})
		},
		StoreFactory: func(_ io.Writer) (auth.Store, error) {
			keyring.MockInit()
			return &auth.KeyringStore{}, nil
		},
	}
}

func TestHelpAndVersionJSON(t *testing.T) {
	out, _, err := execCLI(t, Dependencies{}, "", "--help")
	if err != nil {
		t.Fatalf("--help failed: %v", err)
	}
	if !strings.Contains(out, "tagforge") || !strings.Contains(out, "workspaces") {
		t.Fatalf("unexpected help output: %s", out)
	}

	out, _, err = execCLI(t, Dependencies{}, "", "version", "--format", "json")
	if err != nil {
		t.Fatalf("version --format json failed: %v", err)
	}
	var parsed map[string]string
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("invalid version json %q: %v", out, err)
	}
	if parsed["version"] == "" {
		t.Fatalf("expected version field in JSON output: %v", parsed)
	}
}

func TestCLIAccountsTagsAndSafetyGates(t *testing.T) {
	var publishHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/tagmanager/v2/accounts":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"account": []*tagmanager.Account{
					{AccountId: "100", Name: "Genesis Work", Fingerprint: "fp-a"},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/tagmanager/v2/accounts/100/containers/200/workspaces/3/tags":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tag": []*tagmanager.Tag{
					{TagId: "10", Name: "GA4 - Config", Type: "googtag", Fingerprint: "fp-10"},
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/tagmanager/v2/accounts/100/containers/200/workspaces/3/tags":
			var body tagmanager.Tag
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			body.TagId = "11"
			body.Fingerprint = "fp-11"
			_ = json.NewEncoder(w).Encode(body)
		case r.Method == http.MethodDelete && r.URL.Path == "/tagmanager/v2/accounts/100/containers/200/workspaces/3/tags/10":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/tagmanager/v2/accounts/100/containers/200/versions/5:publish":
			publishHits.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"containerVersion": &tagmanager.ContainerVersion{ContainerVersionId: "5", Name: "v5"},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	deps := mockCLIDeps(t, srv)

	// accounts list table
	out, _, err := execCLI(t, deps, "", "accounts", "list")
	if err != nil {
		t.Fatalf("accounts list failed: %v", err)
	}
	if !strings.Contains(out, "Genesis Work") {
		t.Fatalf("accounts list output missing account name: %s", out)
	}

	// tags list json
	out, _, err = execCLI(t, deps, "", "tags", "list", "--account", "100", "--container", "200", "--workspace", "3", "--format", "json")
	if err != nil {
		t.Fatalf("tags list failed: %v", err)
	}
	if !strings.Contains(out, `"googtag"`) {
		t.Fatalf("tags list JSON missing googtag: %s", out)
	}

	// tags create via stdin
	payload := `{"name":"GA4 - Event - purchase","type":"gaawe"}`
	out, _, err = execCLI(t, deps, payload, "tags", "create", "--account", "100", "--container", "200", "--workspace", "3", "--file", "-", "--format", "json")
	if err != nil {
		t.Fatalf("tags create via stdin failed: %v", err)
	}
	if !strings.Contains(out, `"tagId": "11"`) {
		t.Fatalf("unexpected tags create output: %s", out)
	}

	// tags delete without --confirm fails locally
	_, _, err = execCLI(t, deps, "", "tags", "delete", "--account", "100", "--container", "200", "--workspace", "3", "--tag", "10")
	if !errors.Is(err, safety.ErrBlocked) {
		t.Fatalf("expected ErrBlocked when deleting without --confirm, got %v", err)
	}

	// tags delete with --confirm succeeds
	_, _, err = execCLI(t, deps, "", "tags", "delete", "--account", "100", "--container", "200", "--workspace", "3", "--tag", "10", "--confirm")
	if err != nil {
		t.Fatalf("tags delete with --confirm failed: %v", err)
	}

	// versions publish blocked in default workspace-only mode even with --confirm
	_, _, err = execCLI(t, deps, "", "versions", "publish", "--account", "100", "--container", "200", "--version", "5", "--fingerprint", "fp-5", "--confirm")
	if !errors.Is(err, safety.ErrBlocked) {
		t.Fatalf("expected ErrBlocked for publish in default mode, got %v", err)
	}
	if publishHits.Load() != 0 {
		t.Fatalf("publish should not hit server in workspace-only mode")
	}

	// versions publish succeeds in --mode full --confirm
	out, _, err = execCLI(t, deps, "", "versions", "publish", "--account", "100", "--container", "200", "--version", "5", "--fingerprint", "fp-5", "--mode", "full", "--confirm", "--format", "json")
	if err != nil {
		t.Fatalf("versions publish in full mode failed: %v", err)
	}
	if publishHits.Load() != 1 || !strings.Contains(out, `"containerVersionId": "5"`) {
		t.Fatalf("unexpected publish result: hits=%d out=%s", publishHits.Load(), out)
	}
}

func TestCLIAuthStatusDoesNotLeakSecrets(t *testing.T) {
	deps := Dependencies{
		Getenv: func(string) string { return "" },
		StoreFactory: func(_ io.Writer) (auth.Store, error) {
			keyring.MockInit()
			return &auth.KeyringStore{}, nil
		},
		AuthStatusFn: func(_ context.Context, cfg auth.ResolveConfig) (*auth.StatusReport, error) {
			return &auth.StatusReport{
				Authenticated:        true,
				Source:               auth.SourceStoredOAuth,
				Backend:              "os-keyring",
				Profile:              cfg.Profile,
				Email:                "user@example.com",
				Scopes:               []string{auth.ScopeReadOnly},
				MissingScopesForMode: []string{auth.ScopeEditContainers},
				Mode:                 string(cfg.Mode),
			}, nil
		},
	}

	out, _, err := execCLI(t, deps, "", "auth", "status", "--format", "json")
	if err != nil {
		t.Fatalf("auth status failed: %v", err)
	}
	if !strings.Contains(out, `"authenticated": true`) || !strings.Contains(out, `"user@example.com"`) {
		t.Fatalf("unexpected auth status output: %s", out)
	}
}

func TestCLIAuthLoginAndLogout(t *testing.T) {
	keyring.MockInit()
	store := &auth.KeyringStore{}

	deps := Dependencies{
		Getenv: func(string) string { return "" },
		StoreFactory: func(_ io.Writer) (auth.Store, error) {
			return store, nil
		},
		LoginFn: func(_ context.Context, cfg auth.OAuthLoginConfig) (*auth.StoredToken, error) {
			return &auth.StoredToken{
				Scopes:   cfg.Scopes,
				ClientID: cfg.Client.ClientID,
			}, nil
		},
	}

	out, _, err := execCLI(t, deps, "", "login", "--client-id", "test-cid", "--no-browser", "--format", "json")
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}
	if !strings.Contains(out, `"authenticated": true`) || !strings.Contains(out, `"mode": "workspace-only"`) {
		t.Fatalf("unexpected login output: %s", out)
	}

	if _, err := store.Load("default"); err != nil {
		t.Fatalf("expected token saved in store: %v", err)
	}

	out, _, err = execCLI(t, deps, "", "auth", "logout", "--format", "json")
	if err != nil {
		t.Fatalf("auth logout failed: %v", err)
	}
	if !strings.Contains(out, `"removed": true`) {
		t.Fatalf("unexpected logout output: %s", out)
	}
	if _, err := store.Load("default"); err != auth.ErrTokenNotFound {
		t.Fatalf("expected ErrTokenNotFound after logout, got %v", err)
	}
}

func TestCLIBlueprintsTemplatesAndMCPHelp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/built_in_variables"):
			_ = json.NewEncoder(w).Encode(map[string]any{"builtInVariable": []any{}})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/built_in_variables"):
			_ = json.NewEncoder(w).Encode(map[string]any{"builtInVariable": []*tagmanager.BuiltInVariable{{Type: "event"}, {Type: "pageUrl"}}})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/templates"):
			_ = json.NewEncoder(w).Encode(map[string]any{"template": []any{}})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/templates:import_from_gallery"):
			_ = json.NewEncoder(w).Encode(&tagmanager.CustomTemplate{
				TemplateId: "81",
				Name:       r.URL.Query().Get("galleryRepository"),
			})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/folders"):
			_ = json.NewEncoder(w).Encode(map[string]any{"folder": []any{}})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/variables"):
			_ = json.NewEncoder(w).Encode(map[string]any{"variable": []any{}})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/triggers"):
			_ = json.NewEncoder(w).Encode(map[string]any{"trigger": []any{}})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/tags"):
			_ = json.NewEncoder(w).Encode(map[string]any{"tag": []any{}})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/bulk_update"):
			var body tagmanager.ProposedChange
			_ = json.NewDecoder(r.Body).Decode(&body)
			_ = json.NewEncoder(w).Encode(&tagmanager.BulkUpdateWorkspaceResponse{Changes: body.Changes})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	deps := mockCLIDeps(t, srv)

	// blueprints list
	out, _, err := execCLI(t, deps, "", "blueprints", "list")
	if err != nil {
		t.Fatalf("blueprints list failed: %v", err)
	}
	if !strings.Contains(out, "google-ga4-ecommerce") || !strings.Contains(out, "meta-pixel-web") {
		t.Fatalf("unexpected blueprints list output: %s", out)
	}

	// blueprints show
	out, _, err = execCLI(t, deps, "", "blueprints", "show", "--name", "consent-mode-v2", "--format", "json")
	if err != nil || !strings.Contains(out, `"consent-mode-v2"`) {
		t.Fatalf("blueprints show failed: err=%v out=%s", err, out)
	}

	// blueprints plan
	out, _, err = execCLI(t, deps, "", "blueprints", "plan",
		"--name", "google-ga4-ecommerce",
		"--account", "1", "--container", "2", "--workspace", "3",
		"--param", "measurement_id=G-CLI1234",
		"--format", "json",
	)
	if err != nil || !strings.Contains(out, `"sendEcommerceData"`) {
		t.Fatalf("blueprints plan failed: err=%v out=%s", err, out)
	}

	// blueprints apply
	out, _, err = execCLI(t, deps, "", "blueprints", "apply",
		"--name", "google-ga4-ecommerce",
		"--account", "1", "--container", "2", "--workspace", "3",
		"--param", "measurement_id=G-CLI1234",
		"--format", "json",
	)
	if err != nil || !strings.Contains(out, `"appliedChanges": 4`) {
		t.Fatalf("blueprints apply failed: err=%v out=%s", err, out)
	}

	// templates import-gallery
	out, _, err = execCLI(t, deps, "", "templates", "import-gallery",
		"--account", "1", "--container", "2", "--workspace", "3",
		"--owner", "stape-io", "--repo", "fb-tag", "--format", "json",
	)
	if err != nil || !strings.Contains(out, `"templateId": "81"`) {
		t.Fatalf("templates import-gallery failed: err=%v out=%s", err, out)
	}

	// mcp --help
	out, _, err = execCLI(t, deps, "", "mcp", "--help")
	if err != nil || !strings.Contains(out, "stdio") {
		t.Fatalf("mcp --help failed: err=%v out=%s", err, out)
	}
}

func TestCLIAuditAndTrackingPlan(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/versions:live"):
			_ = json.NewEncoder(w).Encode(&tagmanager.ContainerVersion{
				ContainerVersionId: "12",
				Name:               "Prod v12",
				Tag: []*tagmanager.Tag{
					{TagId: "1", Name: "Legacy UA", Type: "ua", FiringTriggerId: []string{"10"}},
				},
				Trigger: []*tagmanager.Trigger{
					{TriggerId: "10", Name: "CE - purchase", Type: "customEvent", CustomEventFilter: []*tagmanager.Condition{
						{Type: "equals", Parameter: []*tagmanager.Parameter{{Key: "arg0", Value: "{{_event}}"}, {Key: "arg1", Value: "purchase"}}},
					}},
				},
			})
		case strings.HasSuffix(r.URL.Path, "/tags"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tag": []*tagmanager.Tag{
					{TagId: "1", Name: "Legacy UA", Type: "ua", FiringTriggerId: []string{"10"}},
				},
			})
		case strings.HasSuffix(r.URL.Path, "/triggers"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"trigger": []*tagmanager.Trigger{
					{TriggerId: "10", Name: "CE - purchase", Type: "customEvent", CustomEventFilter: []*tagmanager.Condition{
						{Type: "equals", Parameter: []*tagmanager.Parameter{{Key: "arg0", Value: "{{_event}}"}, {Key: "arg1", Value: "purchase"}}},
					}},
				},
			})
		case strings.HasSuffix(r.URL.Path, "/workspaces"),
			strings.HasSuffix(r.URL.Path, "/version_headers"),
			strings.HasSuffix(r.URL.Path, "/variables"),
			strings.HasSuffix(r.URL.Path, "/built_in_variables"),
			strings.HasSuffix(r.URL.Path, "/folders"),
			strings.HasSuffix(r.URL.Path, "/templates"),
			strings.HasSuffix(r.URL.Path, "/clients"),
			strings.HasSuffix(r.URL.Path, "/transformations"):
			_ = json.NewEncoder(w).Encode(map[string]any{})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	deps := mockCLIDeps(t, srv)

	// audit markdown + --fail-on high should print report AND return error
	out, _, err := execCLI(t, deps, "", "audit",
		"--account", "1", "--container", "2", "--workspace", "3",
		"--format", "markdown", "--fail-on", "high",
	)
	if err == nil || !strings.Contains(err.Error(), "audit failed") {
		t.Fatalf("expected fail-on high error, got err=%v", err)
	}
	if !strings.Contains(out, "DEPRECATED_TAG_TYPE") {
		t.Fatalf("expected audit report printed before returning error: %s", out)
	}

	// audit --live --format json
	out, _, err = execCLI(t, deps, "", "audit",
		"--account", "1", "--container", "2", "--live", "--format", "json",
	)
	if err != nil || !strings.Contains(out, `"liveVersionId": "12"`) {
		t.Fatalf("audit --live failed: err=%v out=%s", err, out)
	}

	// tracking-plan markdown default
	out, _, err = execCLI(t, deps, "", "tracking-plan",
		"--account", "1", "--container", "2", "--workspace", "3",
	)
	if err != nil || !strings.Contains(out, "# GTM Tracking Plan") || !strings.Contains(out, `"purchase"`) {
		t.Fatalf("tracking-plan failed: err=%v out=%s", err, out)
	}

	// tracking-plan --snippets shopify --snippets-only
	out, _, err = execCLI(t, deps, "", "tracking-plan",
		"--account", "1", "--container", "2", "--live",
		"--snippets", "shopify", "--snippets-only",
	)
	if err != nil || !strings.Contains(out, "analytics.subscribe") {
		t.Fatalf("tracking-plan shopify snippets-only failed: err=%v out=%s", err, out)
	}
}

func TestCLIServeHelpAndValidation(t *testing.T) {
	out, _, err := execCLI(t, Dependencies{}, "", "serve", "--help")
	if err != nil {
		t.Fatalf("serve --help failed: %v", err)
	}
	for _, flag := range []string{"--http", "--addr", "--auth-token"} {
		if !strings.Contains(out, flag) {
			t.Fatalf("serve --help missing flag %q: %s", flag, out)
		}
	}

	_, _, err = execCLI(t, Dependencies{}, "", "serve", "--http", "--addr", "0.0.0.0:8080")
	if err == nil || !strings.Contains(err.Error(), "--auth-token") {
		t.Fatalf("expected non-loopback refusal mentioning --auth-token, got %v", err)
	}

	_, _, err = execCLI(t, Dependencies{}, "", "serve", "--http=false")
	if err == nil || !strings.Contains(err.Error(), "tagforge mcp") {
		t.Fatalf("expected --http=false error mentioning tagforge mcp, got %v", err)
	}
}
