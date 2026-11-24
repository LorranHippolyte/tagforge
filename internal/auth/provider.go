package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LorranHippolyte/tagforge/internal/safety"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const DefaultTokenInfoURL = "https://oauth2.googleapis.com/tokeninfo"

// AuthSource identifies which credential mechanism resolved authentication.
type AuthSource string

const (
	SourceServiceAccount AuthSource = "service_account"
	SourceRefreshToken   AuthSource = "refresh_token"
	SourceAccessToken    AuthSource = "access_token"
	SourceStoredOAuth    AuthSource = "stored_oauth"
)

// ResolveConfig controls credential resolution precedence and introspection.
type ResolveConfig struct {
	Profile              string
	Mode                 safety.Mode
	AllowUserPermissions bool
	Store                Store
	HTTPClient           *http.Client
	TokenInfoURL         string
	TokenURL             string
	Getenv               func(string) string
	ErrOut               io.Writer
}

// ResolvedAuth holds the resolved TokenSource and safe provenance metadata.
type ResolvedAuth struct {
	TokenSource oauth2.TokenSource
	Source      AuthSource
	Backend     string
	Scopes      []string
}

// StatusReport describes the current authentication state without exposing secrets.
type StatusReport struct {
	Authenticated        bool       `json:"authenticated"`
	Source               AuthSource `json:"source,omitempty"`
	Backend              string     `json:"backend,omitempty"`
	Profile              string     `json:"profile"`
	Email                string     `json:"email,omitempty"`
	ExpiresAt            *time.Time `json:"expires_at,omitempty"`
	Scopes               []string   `json:"scopes,omitempty"`
	MissingScopesForMode []string   `json:"missing_scopes_for_mode,omitempty"`
	Mode                 string     `json:"mode"`
}

// Resolve resolves Google Tag Manager credentials in strict precedence order:
// 1. GOOGLE_SERVICE_ACCOUNT_KEY / GOOGLE_APPLICATION_CREDENTIALS
// 2. GOOGLE_CLIENT_ID + GOOGLE_CLIENT_SECRET + GOOGLE_REFRESH_TOKEN
// 3. GOOGLE_ACCESS_TOKEN
// 4. Stored OAuth token from Store (Keychain / 0600 file fallback)
func Resolve(ctx context.Context, cfg ResolveConfig) (*ResolvedAuth, error) {
	getenv := cfg.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	mode := cfg.Mode
	if mode == "" {
		mode = safety.ModeWorkspaceOnly
	}
	scopes := ScopesForMode(mode, cfg.AllowUserPermissions)

	tokenCtx := ctx
	if cfg.HTTPClient != nil {
		tokenCtx = context.WithValue(ctx, oauth2.HTTPClient, cfg.HTTPClient)
	}

	// 1. Service Account (raw JSON or file path).
	if saRaw := firstNonEmpty(getenv("GOOGLE_SERVICE_ACCOUNT_KEY"), getenv("GOOGLE_APPLICATION_CREDENTIALS")); saRaw != "" {
		jsonBytes, err := loadJSONOrFile(saRaw)
		if err != nil {
			return nil, fmt.Errorf("load service account credentials: %w", err)
		}
		jwtCfg, err := google.JWTConfigFromJSON(jsonBytes, scopes...)
		if err != nil {
			return nil, fmt.Errorf("parse service account credentials: %w", err)
		}
		if subject := strings.TrimSpace(getenv("GOOGLE_IMPERSONATED_USER")); subject != "" {
			jwtCfg.Subject = subject
		}
		if cfg.TokenURL != "" {
			jwtCfg.TokenURL = cfg.TokenURL
		}
		return &ResolvedAuth{
			TokenSource: jwtCfg.TokenSource(tokenCtx),
			Source:      SourceServiceAccount,
			Backend:     "service-account-env",
			Scopes:      scopes,
		}, nil
	}

	tokenURL := cfg.TokenURL
	if tokenURL == "" {
		tokenURL = google.Endpoint.TokenURL
	}

	// 2. OAuth Refresh Token via environment.
	if cid, csec, rtok := strings.TrimSpace(getenv("GOOGLE_CLIENT_ID")), strings.TrimSpace(getenv("GOOGLE_CLIENT_SECRET")), strings.TrimSpace(getenv("GOOGLE_REFRESH_TOKEN")); cid != "" && csec != "" && rtok != "" {
		oauthCfg := &oauth2.Config{
			ClientID:     cid,
			ClientSecret: csec,
			Scopes:       scopes,
			Endpoint: oauth2.Endpoint{
				AuthURL:  google.Endpoint.AuthURL,
				TokenURL: tokenURL,
			},
		}
		ts := oauthCfg.TokenSource(tokenCtx, &oauth2.Token{RefreshToken: rtok})
		return &ResolvedAuth{
			TokenSource: ts,
			Source:      SourceRefreshToken,
			Backend:     "refresh-token-env",
			Scopes:      scopes,
		}, nil
	}

	// 3. Static Access Token (debugging only).
	if accessTok := strings.TrimSpace(getenv("GOOGLE_ACCESS_TOKEN")); accessTok != "" {
		return &ResolvedAuth{
			TokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: accessTok}),
			Source:      SourceAccessToken,
			Backend:     "access-token-env",
		}, nil
	}

	// 4. Stored OAuth token (Keychain / 0600 file fallback).
	store := cfg.Store
	if store == nil {
		var err error
		store, err = NewDefaultStore("", cfg.ErrOut)
		if err != nil {
			return nil, err
		}
	}
	profile := sanitizeProfile(cfg.Profile)
	stored, err := store.Load(profile)
	if err != nil {
		if errors.Is(err, ErrTokenNotFound) {
			return nil, errors.New("not authenticated: run 'tagforge login' or set GOOGLE_SERVICE_ACCOUNT_KEY / GOOGLE_REFRESH_TOKEN")
		}
		return nil, err
	}

	clientID := firstNonEmpty(stored.ClientID, getenv("TAGFORGE_GOOGLE_CLIENT_ID"), getenv("GOOGLE_CLIENT_ID"))
	clientSecret := firstNonEmpty(getenv("TAGFORGE_GOOGLE_CLIENT_SECRET"), getenv("GOOGLE_CLIENT_SECRET"))

	var ts oauth2.TokenSource
	if stored.Token.RefreshToken != "" && clientID != "" {
		oauthCfg := &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Scopes:       stored.Scopes,
			Endpoint: oauth2.Endpoint{
				AuthURL:  google.Endpoint.AuthURL,
				TokenURL: tokenURL,
			},
		}
		base := oauthCfg.TokenSource(tokenCtx, &stored.Token)
		ts = &persistingTokenSource{
			base:     base,
			store:    store,
			profile:  profile,
			previous: stored,
		}
	} else {
		ts = oauth2.StaticTokenSource(&stored.Token)
	}

	return &ResolvedAuth{
		TokenSource: ts,
		Source:      SourceStoredOAuth,
		Backend:     store.Backend(),
		Scopes:      stored.Scopes,
	}, nil
}

// Status verifies the active token against Google's tokeninfo endpoint without exposing secrets.
func Status(ctx context.Context, cfg ResolveConfig) (*StatusReport, error) {
	mode := cfg.Mode
	if mode == "" {
		mode = safety.ModeWorkspaceOnly
	}
	profile := sanitizeProfile(cfg.Profile)

	resolved, err := Resolve(ctx, cfg)
	if err != nil {
		return nil, err
	}
	tok, err := resolved.TokenSource.Token()
	if err != nil {
		return nil, fmt.Errorf("obtain access token: %w", err)
	}

	infoURL := cfg.TokenInfoURL
	if infoURL == "" {
		infoURL = DefaultTokenInfoURL
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}

	form := url.Values{}
	form.Set("access_token", tok.AccessToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, infoURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build tokeninfo request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpClient.Do(req)
	if err != nil {
		// Never return url/form errors that might include credentials.
		return nil, fmt.Errorf("tokeninfo request failed: %s", respErrorSummary(err))
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read tokeninfo response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return &StatusReport{
			Authenticated: false,
			Source:        resolved.Source,
			Backend:       resolved.Backend,
			Profile:       profile,
			Mode:          string(mode),
		}, fmt.Errorf("token rejected by Google tokeninfo (HTTP %d)", resp.StatusCode)
	}

	var payload struct {
		Scope     string `json:"scope"`
		Email     string `json:"email"`
		Exp       string `json:"exp"`
		ExpiresIn string `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parse tokeninfo response: %w", err)
	}

	var granted []string
	if strings.TrimSpace(payload.Scope) != "" {
		granted = strings.Fields(payload.Scope)
	} else {
		granted = resolved.Scopes
	}

	var expiresAt *time.Time
	if payload.Exp != "" {
		if sec, convErr := strconv.ParseInt(payload.Exp, 10, 64); convErr == nil {
			t := time.Unix(sec, 0).UTC()
			expiresAt = &t
		}
	} else if payload.ExpiresIn != "" {
		if sec, convErr := strconv.ParseInt(payload.ExpiresIn, 10, 64); convErr == nil {
			t := time.Now().UTC().Add(time.Duration(sec) * time.Second)
			expiresAt = &t
		}
	} else if !tok.Expiry.IsZero() {
		t := tok.Expiry.UTC()
		expiresAt = &t
	}

	return &StatusReport{
		Authenticated:        true,
		Source:               resolved.Source,
		Backend:              resolved.Backend,
		Profile:              profile,
		Email:                payload.Email,
		ExpiresAt:            expiresAt,
		Scopes:               granted,
		MissingScopesForMode: MissingScopes(granted, mode, cfg.AllowUserPermissions),
		Mode:                 string(mode),
	}, nil
}

type persistingTokenSource struct {
	mu       sync.Mutex
	base     oauth2.TokenSource
	store    Store
	profile  string
	previous *StoredToken
}

func (p *persistingTokenSource) Token() (*oauth2.Token, error) {
	tok, err := p.base.Token()
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.previous == nil || tok.AccessToken != p.previous.Token.AccessToken {
		updated := &StoredToken{
			Token:   *tok,
			SavedAt: time.Now().UTC(),
		}
		if p.previous != nil {
			updated.Scopes = p.previous.Scopes
			updated.ClientID = p.previous.ClientID
			if updated.Token.RefreshToken == "" {
				updated.Token.RefreshToken = p.previous.Token.RefreshToken
			}
		}
		_ = p.store.Save(p.profile, updated)
		p.previous = updated
	}
	return tok, nil
}

func loadJSONOrFile(raw string) ([]byte, error) {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "{") {
		return []byte(trimmed), nil
	}
	return os.ReadFile(trimmed)
}

func respErrorSummary(err error) string {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		return urlErr.Err.Error()
	}
	return err.Error()
}
