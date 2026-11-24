package mcpserver

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/LorranHippolyte/tagforge/internal/safety"
	"github.com/LorranHippolyte/tagforge/internal/version"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Default HTTP server settings for optional remote Streamable HTTP MCP transport.
const (
	DefaultHTTPAddr            = "127.0.0.1:8080"
	DefaultHTTPPath            = "/mcp"
	DefaultHTTPSessionTimeout  = 30 * time.Minute
	DefaultHTTPMaxBodyBytes    = int64(2 << 20)
	DefaultHTTPReadHeaderDelay = 10 * time.Second
)

// HTTPConfig configures the optional Streamable HTTP MCP server.
type HTTPConfig struct {
	Config
	Addr                       string
	Path                       string
	AuthToken                  string
	AllowInsecureNoAuth        bool
	Stateless                  bool
	JSONResponse               bool
	AllowedOrigins             []string
	SessionTimeout             time.Duration
	MaxRequestBodyBytes        int64
	DisableLocalhostProtection bool
	ReadHeaderTimeout          time.Duration
	Logger                     *slog.Logger
}

// ValidateHTTPConfig normalizes defaults and enforces security invariants before binding a socket.
func ValidateHTTPConfig(cfg *HTTPConfig) error {
	if cfg == nil {
		return errors.New("http config is required")
	}
	if strings.TrimSpace(cfg.Addr) == "" {
		cfg.Addr = DefaultHTTPAddr
	}
	if strings.TrimSpace(cfg.Path) == "" {
		cfg.Path = DefaultHTTPPath
	}
	if !strings.HasPrefix(cfg.Path, "/") {
		return fmt.Errorf("invalid HTTP path %q: must start with '/'", cfg.Path)
	}
	if cfg.Path == "/healthz" {
		return fmt.Errorf("invalid HTTP path %q: reserved for health checks", cfg.Path)
	}
	if cfg.SessionTimeout <= 0 {
		cfg.SessionTimeout = DefaultHTTPSessionTimeout
	}
	if cfg.MaxRequestBodyBytes <= 0 {
		cfg.MaxRequestBodyBytes = DefaultHTTPMaxBodyBytes
	}
	if cfg.ReadHeaderTimeout <= 0 {
		cfg.ReadHeaderTimeout = DefaultHTTPReadHeaderDelay
	}

	host, port, err := net.SplitHostPort(cfg.Addr)
	if err != nil {
		return fmt.Errorf("invalid HTTP listen address %q: %w", cfg.Addr, err)
	}
	if strings.TrimSpace(port) == "" {
		return fmt.Errorf("invalid HTTP listen address %q: port is required", cfg.Addr)
	}

	if !isLoopbackHost(host) && strings.TrimSpace(cfg.AuthToken) == "" && !cfg.AllowInsecureNoAuth {
		return fmt.Errorf(
			"refusing to bind HTTP MCP server to non-loopback address %q without --auth-token (or TAGFORGE_HTTP_AUTH_TOKEN); pass --allow-insecure-no-auth only behind an authenticated reverse proxy",
			cfg.Addr,
		)
	}
	return nil
}

func isLoopbackHost(host string) bool {
	h := strings.TrimSpace(strings.Trim(host, "[]"))
	if strings.EqualFold(h, "localhost") || h == "127.0.0.1" || h == "::1" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// NewHTTPHandler constructs a hardened Streamable HTTP MCP handler + /healthz endpoint.
func NewHTTPHandler(cfg HTTPConfig) (http.Handler, error) {
	if err := ValidateHTTPConfig(&cfg); err != nil {
		return nil, err
	}
	srv, err := NewServer(cfg.Config)
	if err != nil {
		return nil, err
	}

	streamHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return srv
	}, &mcp.StreamableHTTPOptions{
		Stateless:                  cfg.Stateless,
		JSONResponse:               cfg.JSONResponse,
		Logger:                     cfg.Logger,
		SessionTimeout:             cfg.SessionTimeout,
		DisableLocalhostProtection: cfg.DisableLocalhostProtection,
		MaxRequestBodyBytes:        cfg.MaxRequestBodyBytes,
	})

	var mcpHandler http.Handler = streamHandler
	if token := strings.TrimSpace(cfg.AuthToken); token != "" {
		mcpHandler = bearerAuthMiddleware(token, mcpHandler)
	}
	if len(cfg.AllowedOrigins) > 0 {
		mcpHandler = corsAllowlistMiddleware(cfg.AllowedOrigins, mcpHandler)
	}

	mode := safety.ModeWorkspaceOnly
	if cfg.Policy != nil {
		mode = cfg.Policy.Mode
	} else if cfg.Client != nil && cfg.Client.Policy() != nil {
		mode = cfg.Client.Policy().Mode
	}
	ver := strings.TrimSpace(cfg.Version)
	if ver == "" {
		ver = version.Current().Version
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			writeJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodHead {
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":  "ok",
			"service": "tagforge",
			"version": ver,
			"mode":    string(mode),
		})
	})
	mux.Handle(cfg.Path, mcpHandler)

	return securityHeadersMiddleware(mux), nil
}

// RunHTTP starts the Streamable HTTP MCP server until ctx cancels, then shuts down gracefully.
func RunHTTP(ctx context.Context, cfg HTTPConfig) error {
	if err := ValidateHTTPConfig(&cfg); err != nil {
		return err
	}
	handler, err := NewHTTPHandler(cfg)
	if err != nil {
		return err
	}

	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.Addr, err)
	}

	httpSrv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- httpSrv.Serve(ln)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
		serveErr := <-errCh
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return serveErr
		}
		return nil
	case serveErr := <-errCh:
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return serveErr
		}
		return nil
	}
}

func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func bearerAuthMiddleware(expectedToken string, next http.Handler) http.Handler {
	expected := []byte(expectedToken)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := parseBearerToken(r.Header.Get("Authorization"))
		if !ok || subtle.ConstantTimeCompare([]byte(got), expected) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="tagforge"`)
			writeJSONError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func parseBearerToken(header string) (string, bool) {
	parts := strings.SplitN(strings.TrimSpace(header), " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}
	token := strings.TrimSpace(parts[1])
	if token == "" {
		return "", false
	}
	return token, true
}

func corsAllowlistMiddleware(allowedOrigins []string, next http.Handler) http.Handler {
	allowed := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		if trimmed := strings.TrimSpace(o); trimmed != "" {
			allowed[trimmed] = true
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin != "" {
			if !allowed[origin] {
				writeJSONError(w, http.StatusForbidden, "origin_not_allowed")
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Add("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept, Mcp-Session-Id, MCP-Protocol-Version")
			w.Header().Set("Access-Control-Expose-Headers", "Mcp-Session-Id")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSONError(w http.ResponseWriter, code int, errCode string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": errCode})
}
