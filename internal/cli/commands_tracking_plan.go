package cli

import (
	"fmt"
	"strings"

	"github.com/LorranHippolyte/tagforge/internal/trackingplan"
	"github.com/spf13/cobra"
)

func newTrackingPlanCmd(app *appContext) *cobra.Command {
	var accountID, containerID, workspaceID, snippetTargetRaw string
	var live, snippetsOnly bool

	cmd := &cobra.Command{
		Use:   "tracking-plan",
		Short: "Generate a living GTM tracking plan and typed dataLayer snippets",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !live && strings.TrimSpace(workspaceID) == "" {
				return fmt.Errorf("either --workspace or --live must be specified")
			}
			target, err := trackingplan.ParseSnippetTarget(snippetTargetRaw)
			if err != nil {
				return err
			}
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}

			plan, err := trackingplan.Generate(cmd.Context(), client, trackingplan.Input{
				AccountID:     accountID,
				ContainerID:   containerID,
				WorkspaceID:   workspaceID,
				Live:          live,
				SnippetTarget: target,
			})
			if err != nil {
				return err
			}

			if snippetsOnly {
				return p.PrintMessage(map[string]any{
					"snippetTarget": plan.SnippetTarget,
					"snippets":      plan.Snippets,
				}, plan.Snippets)
			}

			if p.Format == FormatJSON {
				return p.PrintJSON(plan)
			}
			if cmd.Flags().Changed("format") && p.Format == FormatTable {
				rows := make([][]string, 0, len(plan.Events))
				for _, ev := range plan.Events {
					destNames := make([]string, 0, len(ev.Destinations))
					for _, d := range ev.Destinations {
						destNames = append(destNames, fmt.Sprintf("%s (%s)", d.TagName, d.Platform))
					}
					rows = append(rows, []string{
						ev.EventName,
						ev.TriggerName,
						strings.Join(destNames, ", "),
						strings.Join(ev.DataLayerKeys, ", "),
					})
				}
				return p.PrintTable(plan, []string{"EVENT", "TRIGGER", "DESTINATIONS", "DATALAYER_KEYS"}, rows)
			}

			return p.PrintMarkdown(plan, plan.RenderMarkdown())
		},
	}

	cmd.Flags().StringVar(&accountID, "account", "", "GTM Account ID")
	cmd.Flags().StringVar(&containerID, "container", "", "GTM Container ID")
	cmd.Flags().StringVar(&workspaceID, "workspace", "", "GTM Workspace ID (omit when using --live)")
	cmd.Flags().BoolVar(&live, "live", false, "Generate tracking plan from the currently published live container version")
	cmd.Flags().StringVar(&snippetTargetRaw, "snippets", "ts", "Code snippet flavor: ts, js, nextjs, or shopify")
	cmd.Flags().BoolVar(&snippetsOnly, "snippets-only", false, "Print only the generated application dataLayer code snippets")
	requireFlag(cmd, "account", "container")

	return cmd
}
