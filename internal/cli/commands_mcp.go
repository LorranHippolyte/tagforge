package cli

import (
	"context"
	"fmt"

	"github.com/LorranHippolyte/tagforge/internal/auth"
	"github.com/LorranHippolyte/tagforge/internal/blueprints"
	"github.com/LorranHippolyte/tagforge/internal/mcpserver"
	"github.com/LorranHippolyte/tagforge/internal/safety"
	"github.com/LorranHippolyte/tagforge/internal/version"
	"github.com/spf13/cobra"
)

func newMCPCmd(app *appContext) *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Start the local Model Context Protocol (MCP) server over stdio",
		Long:  "Starts a local stdio MCP server exposing tagforge GTM tools and official blueprints. Diagnostics are written strictly to stderr so stdout remains clean JSON-RPC.",
		RunE: func(cmd *cobra.Command, _ []string) error {
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

			fmt.Fprintf(cmd.ErrOrStderr(), "tagforge MCP server starting on stdio (mode=%s, profile=%s)\n", mode, app.opts.Profile)
			return mcpserver.RunStdio(cmd.Context(), mcpserver.Config{
				Client:       client,
				Policy:       client.Policy(),
				Registry:     reg,
				AuthStatusFn: authStatusFn,
				Version:      version.Current().Version,
			})
		},
	}
}
