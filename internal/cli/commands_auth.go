package cli

import (
	"fmt"
	"strings"

	"github.com/LorranHippolyte/tagforge/internal/auth"
	"github.com/LorranHippolyte/tagforge/internal/safety"
	"github.com/spf13/cobra"
)

type loginFlags struct {
	credentialsFile string
	clientID        string
	clientSecret    string
	noBrowser       bool
}

func newLoginAliasCmd(app *appContext) *cobra.Command {
	lf := &loginFlags{}
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate with Google Tag Manager via local OAuth 2.0 + PKCE",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runLogin(cmd, app, lf)
		},
	}
	bindLoginFlags(cmd, lf)
	return cmd
}

func newAuthCmd(app *appContext) *cobra.Command {
	authCmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage authentication credentials and inspect token status",
	}

	lf := &loginFlags{}
	loginCmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate with Google Tag Manager via local OAuth 2.0 + PKCE",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runLogin(cmd, app, lf)
		},
	}
	bindLoginFlags(loginCmd, lf)

	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Inspect current authentication source and scopes",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := app.printer(cmd)
			if err != nil {
				return err
			}
			mode, err := safety.ParseMode(app.opts.Mode)
			if err != nil {
				return err
			}
			store, err := app.deps.StoreFactory(cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			report, err := app.deps.AuthStatusFn(cmd.Context(), auth.ResolveConfig{
				Profile:              app.opts.Profile,
				Mode:                 mode,
				AllowUserPermissions: app.opts.AllowUserPermissions,
				Store:                store,
				Getenv:               app.deps.Getenv,
				ErrOut:               cmd.ErrOrStderr(),
			})
			if err != nil {
				return err
			}
			rows := [][]string{
				{
					boolStr(report.Authenticated),
					string(report.Source),
					orDash(report.Backend),
					report.Profile,
					orDash(report.Email),
					formatTimePtr(report.ExpiresAt),
					fmt.Sprintf("%d missing", len(report.MissingScopesForMode)),
				},
			}
			return p.PrintTable(
				report,
				[]string{"AUTHENTICATED", "SOURCE", "BACKEND", "PROFILE", "EMAIL", "EXPIRES_AT", "SCOPE_CHECK"},
				rows,
			)
		},
	}

	logoutCmd := &cobra.Command{
		Use:   "logout",
		Short: "Remove stored OAuth credentials for the active profile",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := app.printer(cmd)
			if err != nil {
				return err
			}
			store, err := app.deps.StoreFactory(cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			if err := store.Delete(app.opts.Profile); err != nil {
				return err
			}
			res := map[string]any{
				"profile": app.opts.Profile,
				"removed": true,
			}
			return p.PrintMessage(res, fmt.Sprintf("Removed stored credentials for profile %q.", app.opts.Profile))
		},
	}

	authCmd.AddCommand(loginCmd, statusCmd, logoutCmd)
	return authCmd
}

func bindLoginFlags(cmd *cobra.Command, lf *loginFlags) {
	cmd.Flags().StringVar(&lf.credentialsFile, "credentials-file", "", "Path to Google Cloud OAuth Desktop JSON file")
	cmd.Flags().StringVar(&lf.clientID, "client-id", "", "Google OAuth Client ID (env: TAGFORGE_GOOGLE_CLIENT_ID)")
	cmd.Flags().StringVar(&lf.clientSecret, "client-secret", "", "Google OAuth Client Secret (env: TAGFORGE_GOOGLE_CLIENT_SECRET)")
	cmd.Flags().BoolVar(&lf.noBrowser, "no-browser", false, "Print authorization URL to stderr without launching browser")
}

func runLogin(cmd *cobra.Command, app *appContext, lf *loginFlags) error {
	p, err := app.printer(cmd)
	if err != nil {
		return err
	}
	mode, err := safety.ParseMode(app.opts.Mode)
	if err != nil {
		return err
	}
	oauthClient, err := auth.ResolveOAuthClientConfig(lf.credentialsFile, lf.clientID, lf.clientSecret, app.deps.Getenv)
	if err != nil {
		return err
	}
	scopes := auth.ScopesForMode(mode, app.opts.AllowUserPermissions)

	tok, err := app.deps.LoginFn(cmd.Context(), auth.OAuthLoginConfig{
		Client:    *oauthClient,
		Scopes:    scopes,
		NoBrowser: lf.noBrowser,
		ErrOut:    cmd.ErrOrStderr(),
	})
	if err != nil {
		return err
	}

	store, err := app.deps.StoreFactory(cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	if err := store.Save(app.opts.Profile, tok); err != nil {
		return err
	}

	summary := map[string]any{
		"authenticated": true,
		"profile":       app.opts.Profile,
		"backend":       store.Backend(),
		"mode":          mode,
		"scopes":        scopes,
	}
	msg := fmt.Sprintf("Authenticated profile %q via %s (mode: %s, scopes: %s).",
		app.opts.Profile, store.Backend(), mode, strings.Join(scopes, ", "))
	return p.PrintMessage(summary, msg)
}
