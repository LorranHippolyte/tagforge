package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// DesktopClientConfig holds OAuth 2.0 client credentials for local desktop login.
type DesktopClientConfig struct {
	ClientID     string
	ClientSecret string
	AuthURL      string
	TokenURL     string
}

type desktopCredentialsEnvelope struct {
	Installed *desktopCredentialsBody `json:"installed"`
	Web       *desktopCredentialsBody `json:"web"`
}

type desktopCredentialsBody struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	AuthURI      string `json:"auth_uri"`
	TokenURI     string `json:"token_uri"`
}

// ParseDesktopCredentialsJSON parses Google Cloud Console OAuth JSON credentials
// (supporting both "installed" and "web" client JSON formats).
func ParseDesktopCredentialsJSON(data []byte) (*DesktopClientConfig, error) {
	var env desktopCredentialsEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("parse OAuth credentials JSON: %w", err)
	}
	body := env.Installed
	if body == nil {
		body = env.Web
	}
	if body == nil || strings.TrimSpace(body.ClientID) == "" {
		return nil, errors.New("OAuth credentials JSON missing 'installed' or 'web' client_id")
	}
	authURL := strings.TrimSpace(body.AuthURI)
	if authURL == "" {
		authURL = google.Endpoint.AuthURL
	}
	tokenURL := strings.TrimSpace(body.TokenURI)
	if tokenURL == "" {
		tokenURL = google.Endpoint.TokenURL
	}
	return &DesktopClientConfig{
		ClientID:     strings.TrimSpace(body.ClientID),
		ClientSecret: strings.TrimSpace(body.ClientSecret),
		AuthURL:      authURL,
		TokenURL:     tokenURL,
	}, nil
}

// ResolveOAuthClientConfig loads OAuth client config from a credentials file, explicit flags,
// or TAGFORGE_GOOGLE_CLIENT_ID / GOOGLE_CLIENT_ID environment variables.
func ResolveOAuthClientConfig(credentialsFile, clientID, clientSecret string, getenv func(string) string) (*DesktopClientConfig, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	if strings.TrimSpace(credentialsFile) != "" {
		data, err := os.ReadFile(credentialsFile)
		if err != nil {
			return nil, fmt.Errorf("read credentials file: %w", err)
		}
		return ParseDesktopCredentialsJSON(data)
	}

	id := strings.TrimSpace(clientID)
	secret := strings.TrimSpace(clientSecret)
	if id == "" {
		id = firstNonEmpty(getenv("TAGFORGE_GOOGLE_CLIENT_ID"), getenv("GOOGLE_CLIENT_ID"))
	}
	if secret == "" {
		secret = firstNonEmpty(getenv("TAGFORGE_GOOGLE_CLIENT_SECRET"), getenv("GOOGLE_CLIENT_SECRET"))
	}
	if id == "" {
		return nil, errors.New("missing OAuth client ID: provide --credentials-file, --client-id, or TAGFORGE_GOOGLE_CLIENT_ID")
	}

	return &DesktopClientConfig{
		ClientID:     id,
		ClientSecret: secret,
		AuthURL:      google.Endpoint.AuthURL,
		TokenURL:     google.Endpoint.TokenURL,
	}, nil
}

// GenerateCodeVerifier creates a cryptographically random RFC 7636 PKCE verifier.
func GenerateCodeVerifier() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate PKCE verifier: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// CodeChallengeS256 derives the S256 PKCE code challenge for verifier.
func CodeChallengeS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// GenerateState creates a 32-byte cryptographically random OAuth state token.
func GenerateState() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate OAuth state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// OAuthLoginConfig configures the local loopback OAuth 2.0 + PKCE login flow.
type OAuthLoginConfig struct {
	Client      DesktopClientConfig
	Scopes      []string
	NoBrowser   bool
	ErrOut      io.Writer
	OpenBrowser func(string) error
	HTTPClient  *http.Client
}

// LoginBrowser executes an OAuth 2.0 Authorization Code flow with PKCE using an ephemeral
// 127.0.0.1 loopback HTTP server. Prompts and URLs are written exclusively to ErrOut (never stdout).
func LoginBrowser(ctx context.Context, cfg OAuthLoginConfig) (*StoredToken, error) {
	if strings.TrimSpace(cfg.Client.ClientID) == "" {
		return nil, errors.New("OAuth client ID is required")
	}
	errOut := cfg.ErrOut
	if errOut == nil {
		errOut = os.Stderr
	}

	state, err := GenerateState()
	if err != nil {
		return nil, err
	}
	verifier, err := GenerateCodeVerifier()
	if err != nil {
		return nil, err
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("bind local OAuth callback listener: %w", err)
	}
	defer listener.Close()

	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return nil, errors.New("unexpected non-TCP loopback address")
	}
	redirectURL := fmt.Sprintf("http://127.0.0.1:%d/callback", tcpAddr.Port)

	authURL := cfg.Client.AuthURL
	if authURL == "" {
		authURL = google.Endpoint.AuthURL
	}
	tokenURL := cfg.Client.TokenURL
	if tokenURL == "" {
		tokenURL = google.Endpoint.TokenURL
	}

	oauthCfg := &oauth2.Config{
		ClientID:     cfg.Client.ClientID,
		ClientSecret: cfg.Client.ClientSecret,
		RedirectURL:  redirectURL,
		Scopes:       cfg.Scopes,
		Endpoint: oauth2.Endpoint{
			AuthURL:  authURL,
			TokenURL: tokenURL,
		},
	}

	type callbackResult struct {
		code string
		err  error
	}
	resCh := make(chan callbackResult, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if errParam := q.Get("error"); errParam != "" {
			desc := q.Get("error_description")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintf(w, "<html><body><h3>Authentication Failed</h3><p>%s</p></body></html>", html.EscapeString(errParam))
			select {
			case resCh <- callbackResult{err: fmt.Errorf("oauth authorization error %q: %s", errParam, desc)}:
			default:
			}
			return
		}
		if q.Get("state") != state {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, "<html><body><h3>Invalid OAuth state</h3></body></html>")
			select {
			case resCh <- callbackResult{err: errors.New("oauth state mismatch")}:
			default:
			}
			return
		}
		code := q.Get("code")
		if code == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, "<html><body><h3>Missing authorization code</h3></body></html>")
			select {
			case resCh <- callbackResult{err: errors.New("oauth callback missing authorization code")}:
			default:
			}
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<html><body><h3>Authentication complete.</h3><p>You can close this tab and return to tagforge.</p></body></html>")
		select {
		case resCh <- callbackResult{code: code}:
		default:
		}
	})

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		if serveErr := srv.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			select {
			case resCh <- callbackResult{err: serveErr}:
			default:
			}
		}
	}()
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	consentURL := oauthCfg.AuthCodeURL(
		state,
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("prompt", "consent"),
		oauth2.S256ChallengeOption(verifier),
	)

	fmt.Fprintf(errOut, "Open the following URL in your browser to authorize tagforge:\n\n  %s\n\n", consentURL)

	openFn := cfg.OpenBrowser
	if openFn == nil {
		openFn = defaultOpenBrowser
	}
	if !cfg.NoBrowser {
		if openErr := openFn(consentURL); openErr != nil {
			fmt.Fprintf(errOut, "warning: could not launch browser automatically (%v); open the URL above manually\n", openErr)
		}
	}

	var cb callbackResult
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case cb = <-resCh:
	}
	if cb.err != nil {
		return nil, cb.err
	}

	exchangeCtx := ctx
	if cfg.HTTPClient != nil {
		exchangeCtx = context.WithValue(ctx, oauth2.HTTPClient, cfg.HTTPClient)
	}
	tok, err := oauthCfg.Exchange(exchangeCtx, cb.code, oauth2.VerifierOption(verifier))
	if err != nil {
		return nil, fmt.Errorf("exchange authorization code: %w", err)
	}

	return &StoredToken{
		Token:    *tok,
		Scopes:   append([]string(nil), cfg.Scopes...),
		ClientID: cfg.Client.ClientID,
		SavedAt:  time.Now().UTC(),
	}, nil
}

func defaultOpenBrowser(targetURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", targetURL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", targetURL)
	default:
		cmd = exec.Command("xdg-open", targetURL)
	}
	return cmd.Start()
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
