package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/LorranHippolyte/tagforge/internal/safety"
	"github.com/zalando/go-keyring"
	"golang.org/x/oauth2"
)

func TestScopesForMode(t *testing.T) {
	ro := ScopesForMode(safety.ModeReadOnly, false)
	if !slices.Equal(ro, []string{ScopeReadOnly}) {
		t.Fatalf("read-only scopes = %v, want [%s]", ro, ScopeReadOnly)
	}

	ws := ScopesForMode(safety.ModeWorkspaceOnly, false)
	if slices.Contains(ws, ScopePublish) || slices.Contains(ws, ScopeManageUsers) || slices.Contains(ws, ScopeDeleteContainers) {
		t.Fatalf("workspace-only must not include publish/delete/manage.users: %v", ws)
	}
	if !slices.Contains(ws, ScopeEditContainers) || !slices.Contains(ws, ScopeEditContainerVersions) {
		t.Fatalf("workspace-only missing edit scopes: %v", ws)
	}

	full := ScopesForMode(safety.ModeFull, false)
	if !slices.Contains(full, ScopePublish) || !slices.Contains(full, ScopeDeleteContainers) {
		t.Fatalf("full missing publish/delete scopes: %v", full)
	}
	if slices.Contains(full, ScopeManageUsers) {
		t.Fatalf("full must exclude manage.users unless explicitly allowed: %v", full)
	}

	fullUsers := ScopesForMode(safety.ModeFull, true)
	if !slices.Contains(fullUsers, ScopeManageUsers) {
		t.Fatalf("expected manage.users when allowUserPermissions=true: %v", fullUsers)
	}
}

func TestKeyringStoreMockRoundTrip(t *testing.T) {
	keyring.MockInit()

	store := &KeyringStore{}
	tok := &StoredToken{
		Token: oauth2.Token{
			AccessToken:  "mock-access-token",
			RefreshToken: "mock-refresh-token",
			Expiry:       time.Now().UTC().Add(time.Hour),
		},
		Scopes:   []string{ScopeReadOnly},
		ClientID: "client-123",
	}

	if err := store.Save("work", tok); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := store.Load("work")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.Token.AccessToken != "mock-access-token" || loaded.ClientID != "client-123" {
		t.Fatalf("unexpected loaded token: %+v", loaded)
	}

	if err := store.Delete("work"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if _, err := store.Load("work"); err != ErrTokenNotFound {
		t.Fatalf("expected ErrTokenNotFound after delete, got %v", err)
	}
}

func TestFileStorePermissionsAndRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewFileStore(tmpDir)
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}

	tok := &StoredToken{
		Token: oauth2.Token{
			AccessToken:  "file-access",
			RefreshToken: "file-refresh",
		},
		ClientID: "client-abc",
		Scopes:   []string{ScopeReadOnly, ScopeEditContainers},
	}

	if err := store.Save("clientA", tok); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	info, err := os.Stat(filepath.Join(tmpDir, "tokens", "clientA.json"))
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("token file permissions = %o, want 0600", perm)
	}

	loaded, err := store.Load("clientA")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.Token.RefreshToken != "file-refresh" {
		t.Fatalf("unexpected refresh token: %q", loaded.Token.RefreshToken)
	}

	if err := store.Delete("clientA"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if _, err := store.Load("clientA"); err != ErrTokenNotFound {
		t.Fatalf("expected ErrTokenNotFound, got %v", err)
	}
}

func TestPKCEAndLoopbackLoginFlow(t *testing.T) {
	verifier, err := GenerateCodeVerifier()
	if err != nil {
		t.Fatalf("GenerateCodeVerifier failed: %v", err)
	}
	challenge := CodeChallengeS256(verifier)
	sum := sha256.Sum256([]byte(verifier))
	if want := base64.RawURLEncoding.EncodeToString(sum[:]); challenge != want {
		t.Fatalf("CodeChallengeS256 = %q, want %q", challenge, want)
	}

	var gotVerifier string
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm failed: %v", err)
		}
		gotVerifier = r.Form.Get("code_verifier")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "test-access-123",
			"refresh_token": "test-refresh-456",
			"token_type":    "Bearer",
			"expires_in":    3600,
		})
	}))
	defer tokenSrv.Close()

	var errBuf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var capturedChallenge string
	stored, err := LoginBrowser(ctx, OAuthLoginConfig{
		Client: DesktopClientConfig{
			ClientID:     "desktop-client-id",
			ClientSecret: "desktop-secret",
			AuthURL:      "https://accounts.google.com/o/oauth2/v2/auth",
			TokenURL:     tokenSrv.URL,
		},
		Scopes: []string{ScopeReadOnly},
		ErrOut: &errBuf,
		OpenBrowser: func(consentURL string) error {
			u, parseErr := url.Parse(consentURL)
			if parseErr != nil {
				return parseErr
			}
			q := u.Query()
			capturedChallenge = q.Get("code_challenge")
			cbURL, parseErr := url.Parse(q.Get("redirect_uri"))
			if parseErr != nil {
				return parseErr
			}
			cbQ := cbURL.Query()
			cbQ.Set("state", q.Get("state"))
			cbQ.Set("code", "auth-code-xyz")
			cbURL.RawQuery = cbQ.Encode()

			go func() {
				resp, getErr := http.Get(cbURL.String())
				if getErr == nil {
					_ = resp.Body.Close()
				}
			}()
			return nil
		},
	})
	if err != nil {
		t.Fatalf("LoginBrowser failed: %v", err)
	}

	if capturedChallenge == "" {
		t.Fatalf("expected non-empty code_challenge in consent URL")
	}
	if gotVerifier == "" || CodeChallengeS256(gotVerifier) != capturedChallenge {
		t.Fatalf("PKCE verifier/challenge mismatch: challenge=%q verifier=%q", capturedChallenge, gotVerifier)
	}
	if stored.Token.AccessToken != "test-access-123" || stored.Token.RefreshToken != "test-refresh-456" {
		t.Fatalf("unexpected stored token: %+v", stored)
	}
}

func TestStatusDoesNotLeakTokenAndReportsMissingScopes(t *testing.T) {
	keyring.MockInit()

	const secretAccessToken = "ya29.super-secret-token-never-expose"
	tokenInfoSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.Form.Get("access_token") != secretAccessToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"scope":      ScopeReadOnly,
			"email":      "analyst@example.com",
			"expires_in": "3599",
		})
	}))
	defer tokenInfoSrv.Close()

	report, err := Status(context.Background(), ResolveConfig{
		Profile:      "default",
		Mode:         safety.ModeWorkspaceOnly,
		HTTPClient:   tokenInfoSrv.Client(),
		TokenInfoURL: tokenInfoSrv.URL,
		Getenv: func(key string) string {
			if key == "GOOGLE_ACCESS_TOKEN" {
				return secretAccessToken
			}
			return ""
		},
	})
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if !report.Authenticated || report.Email != "analyst@example.com" || report.Source != SourceAccessToken {
		t.Fatalf("unexpected report: %+v", report)
	}
	if !slices.Contains(report.MissingScopesForMode, ScopeEditContainers) {
		t.Fatalf("expected missing edit.containers scope, got %v", report.MissingScopesForMode)
	}

	rawReport, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("json.Marshal report failed: %v", err)
	}
	if strings.Contains(string(rawReport), secretAccessToken) {
		t.Fatalf("secret access token leaked in JSON status report")
	}
}

func TestParseDesktopCredentialsJSON(t *testing.T) {
	raw := `{"installed":{"client_id":"cid-1","client_secret":"csec-1","auth_uri":"https://auth.example","token_uri":"https://tok.example"}}`
	cfg, err := ParseDesktopCredentialsJSON([]byte(raw))
	if err != nil {
		t.Fatalf("ParseDesktopCredentialsJSON failed: %v", err)
	}
	if cfg.ClientID != "cid-1" || cfg.ClientSecret != "csec-1" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}
