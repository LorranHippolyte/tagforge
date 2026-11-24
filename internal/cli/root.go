package cli

import (
	"context"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/LorranHippolyte/tagforge/internal/auth"
	"github.com/LorranHippolyte/tagforge/internal/gtm"
	"github.com/LorranHippolyte/tagforge/internal/safety"
	"github.com/LorranHippolyte/tagforge/internal/version"
	"github.com/spf13/cobra"
)

// GlobalOptions holds root CLI flags shared by all subcommands.
type GlobalOptions struct {
	Mode                 string
	Format               string
	Profile              string
	Confirm              bool
	AllowUserPermissions bool
	NoCache              bool
	Fresh                bool
}

// Dependencies allows injecting mocks cleanly in unit/integration tests.
type Dependencies struct {
	ClientFactory func(ctx context.Context, opts GlobalOptions, errOut io.Writer) (*gtm.Client, error)
	StoreFactory  func(errOut io.Writer) (auth.Store, error)
	AuthStatusFn  func(ctx context.Context, cfg auth.ResolveConfig) (*auth.StatusReport, error)
	LoginFn       func(ctx context.Context, cfg auth.OAuthLoginConfig) (*auth.StoredToken, error)
	Getenv        func(string) string
}

type appContext struct {
	opts GlobalOptions
	deps Dependencies
}

// NewRootCmd constructs the root tagforge Cobra command.
func NewRootCmd(deps Dependencies) *cobra.Command {
	if deps.Getenv == nil {
		deps.Getenv = os.Getenv
	}
	if deps.StoreFactory == nil {
		deps.StoreFactory = func(errOut io.Writer) (auth.Store, error) {
			return auth.NewDefaultStore("", errOut)
		}
	}
	if deps.AuthStatusFn == nil {
		deps.AuthStatusFn = auth.Status
	}
	if deps.LoginFn == nil {
		deps.LoginFn = auth.LoginBrowser
	}
	if deps.ClientFactory == nil {
		deps.ClientFactory = defaultClientFactory(deps)
	}

	defaultMode := strings.TrimSpace(deps.Getenv("TAGFORGE_MODE"))
	if defaultMode == "" {
		defaultMode = string(safety.ModeWorkspaceOnly)
	}
	defaultAllowUsers, _ := strconv.ParseBool(strings.TrimSpace(deps.Getenv("TAGFORGE_ALLOW_USER_PERMISSIONS")))

	app := &appContext{
		opts: GlobalOptions{
			Mode:                 defaultMode,
			Format:               string(FormatTable),
			Profile:              "default",
			AllowUserPermissions: defaultAllowUsers,
		},
		deps: deps,
	}

	root := &cobra.Command{
		Use:           "tagforge",
		Short:         "Safe, standards-compliant Google Tag Manager CLI",
		Long:          "tagforge manages, blueprints, and audits Google Tag Manager containers safely from your terminal.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	pf := root.PersistentFlags()
	pf.StringVar(&app.opts.Mode, "mode", app.opts.Mode, "Safety mode: read-only, workspace-only, or full (env: TAGFORGE_MODE)")
	pf.StringVar(&app.opts.Format, "format", app.opts.Format, "Output format: table or json")
	pf.StringVar(&app.opts.Profile, "profile", app.opts.Profile, "Auth profile name")
	pf.BoolVar(&app.opts.Confirm, "confirm", false, "Confirm destructive operations or live publishing")
	pf.BoolVar(&app.opts.AllowUserPermissions, "allow-user-permissions", app.opts.AllowUserPermissions, "Unlock user_permission operations (env: TAGFORGE_ALLOW_USER_PERMISSIONS)")
	pf.BoolVar(&app.opts.NoCache, "no-cache", false, "Disable in-memory snapshot caching")
	pf.BoolVar(&app.opts.Fresh, "fresh", false, "Bypass cached snapshots for list requests")

	root.AddCommand(newVersionCmd(app))
	root.AddCommand(newLoginAliasCmd(app))
	root.AddCommand(newAuthCmd(app))
	root.AddCommand(newAccountsCmd(app))
	root.AddCommand(newContainersCmd(app))
	root.AddCommand(newWorkspacesCmd(app))
	root.AddCommand(newTagsCmd(app))
	root.AddCommand(newTriggersCmd(app))
	root.AddCommand(newVariablesCmd(app))
	root.AddCommand(newBuiltInVariablesCmd(app))
	root.AddCommand(newFoldersCmd(app))
	root.AddCommand(newTemplatesCmd(app))
	root.AddCommand(newClientsCmd(app))
	root.AddCommand(newTransformationsCmd(app))
	root.AddCommand(newVersionsCmd(app))

	return root
}

func defaultClientFactory(deps Dependencies) func(ctx context.Context, opts GlobalOptions, errOut io.Writer) (*gtm.Client, error) {
	return func(ctx context.Context, opts GlobalOptions, errOut io.Writer) (*gtm.Client, error) {
		mode, err := safety.ParseMode(opts.Mode)
		if err != nil {
			return nil, err
		}
		store, err := deps.StoreFactory(errOut)
		if err != nil {
			return nil, err
		}
		resolved, err := auth.Resolve(ctx, auth.ResolveConfig{
			Profile:              opts.Profile,
			Mode:                 mode,
			AllowUserPermissions: opts.AllowUserPermissions,
			Store:                store,
			Getenv:               deps.Getenv,
			ErrOut:               errOut,
		})
		if err != nil {
			return nil, err
		}

		qps := gtm.DefaultRateLimitQPS
		if rawQPS := strings.TrimSpace(deps.Getenv("TAGFORGE_RATE_LIMIT_QPS")); rawQPS != "" {
			if parsed, parseErr := strconv.ParseFloat(rawQPS, 64); parseErr == nil && parsed > 0 {
				qps = parsed
			}
		}

		return gtm.NewClient(ctx, gtm.ClientConfig{
			TokenSource: resolved.TokenSource,
			Policy: &safety.Policy{
				Mode:                 mode,
				AllowUserPermissions: opts.AllowUserPermissions,
			},
			Scheduler: gtm.NewScheduler(gtm.SchedulerOptions{QPS: qps}),
			Cache:     gtm.NewSnapshotCache(!opts.NoCache, gtm.DefaultCacheTTL),
		})
	}
}

func (a *appContext) printer(cmd *cobra.Command) (*Printer, error) {
	f, err := ParseFormat(a.opts.Format)
	if err != nil {
		return nil, err
	}
	return &Printer{
		Out:    cmd.OutOrStdout(),
		Err:    cmd.ErrOrStderr(),
		Format: f,
	}, nil
}

func (a *appContext) client(cmd *cobra.Command) (*gtm.Client, error) {
	return a.deps.ClientFactory(cmd.Context(), a.opts, cmd.ErrOrStderr())
}

func newVersionCmd(app *appContext) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print tagforge build version metadata",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := app.printer(cmd)
			if err != nil {
				return err
			}
			info := version.Current()
			return p.PrintMessage(info, info.String())
		},
	}
}

func formatTimePtr(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}

func requireFlag(cmd *cobra.Command, names ...string) {
	for _, n := range names {
		_ = cmd.MarkFlagRequired(n)
	}
}

func orDash(v string) string {
	if strings.TrimSpace(v) == "" {
		return "-"
	}
	return v
}

func boolStr(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
