package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/LorranHippolyte/tagforge/internal/auth"
	"github.com/LorranHippolyte/tagforge/internal/blueprints"
	"github.com/LorranHippolyte/tagforge/internal/mcpserver"
	"github.com/LorranHippolyte/tagforge/internal/safety"
	"github.com/LorranHippolyte/tagforge/internal/version"
	"github.com/spf13/cobra"
)

func newServeCmd(app *appContext) *cobra.Command {
	defaultAddr := strings.TrimSpace(app.deps.Getenv("TAGFORGE_HTTP_ADDR"))
	if defaultAddr == "" {
		defaultAddr = mcpserver.DefaultHTTPAddr
	}
	defaultToken := strings.TrimSpace(app.deps.Getenv("TAGFORGE_HTTP_AUTH_TOKEN"))

	var (
		useHTTP                    = true
		addr                       = defaultAddr
		httpPath                   = mcpserver.DefaultHTTPPath
		authToken                  = defaultToken
		allowInsecureNoAuth        bool
		stateless                  bool
		jsonResponse               bool
		allowedOrigins             []string
		disableLocalhostProtection bool
	)

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run optional self-hosted MCP server over Streamable HTTP",
		Long: "Starts an optional remote Streamable HTTP MCP server exposing tagforge tools, blueprints, resources, and prompts alongside an unauthenticated /healthz probe. " +
			"Non-loopback binds require --auth-token (or TAGFORGE_HTTP_AUTH_TOKEN) unless --allow-insecure-no-auth is passed explicitly.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !useHTTP {
				return errors.New("streamable HTTP disabled (--http=false); use 'tagforge mcp' for local stdio transport")
			}

			httpCfg := mcpserver.HTTPConfig{
				Addr:                       addr,
				Path:                       httpPath,
				AuthToken:                  authToken,
				AllowInsecureNoAuth:        allowInsecureNoAuth,
				Stateless:                  stateless,
				JSONResponse:               jsonResponse,
				AllowedOrigins:             allowedOrigins,
				DisableLocalhostProtection: disableLocalhostProtection,
			}
			// Fail fast on insecure/invalid HTTP flags before touching Google credentials.
			if err := mcpserver.ValidateHTTPConfig(&httpCfg); err != nil {
				return err
			}

			mode, err := safety.ParseMode(app.opts.Mode)
			if err != nil {
				return err
			}
			client, err := app.client(cmd)
			if err != nil {
				return err
			}
			reg, err := blueprints.NewDefaultRegistry()
			if err != nil {
				return err
			}

			authStatusFn := func(statusCtx context.Context) (*auth.StatusReport, error) {
				store, storeErr := app.deps.StoreFactory(cmd.ErrOrStderr())
				if storeErr != nil {
					return nil, storeErr
				}
				return app.deps.AuthStatusFn(statusCtx, auth.ResolveConfig{
					Profile:              app.opts.Profile,
					Mode:                 mode,
					AllowUserPermissions: app.opts.AllowUserPermissions,
					Store:                store,
					Getenv:               app.deps.Getenv,
					ErrOut:               cmd.ErrOrStderr(),
				})
			}

			ver := version.Current().Version
			httpCfg.Config = mcpserver.Config{
				Client:       client,
				Policy:       client.Policy(),
				Registry:     reg,
				AuthStatusFn: authStatusFn,
				Version:      ver,
			}

			authLabel := "none(loopback)"
			if strings.TrimSpace(httpCfg.AuthToken) != "" {
				authLabel = "bearer"
			} else if httpCfg.AllowInsecureNoAuth {
				authLabel = "disabled-proxy"
			}

			fmt.Fprintf(cmd.ErrOrStderr(), "tagforge HTTP MCP server listening on %s%s (mode=%s, auth=%s)\n", httpCfg.Addr, httpCfg.Path, mode, authLabel)
			return mcpserver.RunHTTP(cmd.Context(), httpCfg)
		},
	}

	f := cmd.Flags()
	f.BoolVar(&useHTTP, "http", useHTTP, "Serve MCP over Streamable HTTP")
	f.StringVar(&addr, "addr", addr, "HTTP listen address (env: TAGFORGE_HTTP_ADDR)")
	f.StringVar(&httpPath, "path", httpPath, "MCP Streamable HTTP endpoint path")
	f.StringVar(&authToken, "auth-token", authToken, "Bearer token protecting the MCP endpoint (env: TAGFORGE_HTTP_AUTH_TOKEN)")
	f.BoolVar(&allowInsecureNoAuth, "allow-insecure-no-auth", false, "Allow non-loopback HTTP bind without a Bearer token (only behind an authenticated reverse proxy)")
	f.BoolVar(&stateless, "stateless", false, "Run Streamable HTTP handler in stateless mode (disables Mcp-Session-Id)")
	f.BoolVar(&jsonResponse, "json-response", false, "Return application/json instead of text/event-stream for Streamable HTTP responses")
	f.StringSliceVar(&allowedOrigins, "allow-origin", nil, "Allowed CORS Origin (repeatable)")
	f.BoolVar(&disableLocalhostProtection, "disable-localhost-protection", false, "Disable MCP SDK localhost DNS-rebinding Host check")

	return cmd
}
