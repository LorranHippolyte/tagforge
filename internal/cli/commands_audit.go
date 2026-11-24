package cli

import (
	"fmt"
	"strings"

	"github.com/LorranHippolyte/tagforge/internal/auditor"
	"github.com/spf13/cobra"
)

func newAuditCmd(app *appContext) *cobra.Command {
	var accountID, containerID, workspaceID, minSeverityRaw, failOnRaw string
	var live bool

	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Run deterministic privacy, security, Consent Mode v2, and hygiene audit on GTM",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !live && strings.TrimSpace(workspaceID) == "" {
				return fmt.Errorf("either --workspace or --live must be specified")
			}
			minSev, err := auditor.ParseSeverity(minSeverityRaw)
			if err != nil {
				return err
			}
			var failSev auditor.Severity
			if strings.TrimSpace(failOnRaw) != "" {
				failSev, err = auditor.ParseSeverity(failOnRaw)
				if err != nil {
					return err
				}
			}

			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}

			report, err := auditor.Run(cmd.Context(), client, auditor.Input{
				AccountID:   accountID,
				ContainerID: containerID,
				WorkspaceID: workspaceID,
				Live:        live,
				MinSeverity: minSev,
			})
			if err != nil {
				return err
			}

			switch p.Format {
			case FormatJSON:
				if err := p.PrintJSON(report); err != nil {
					return err
				}
			case FormatMarkdown:
				if err := p.PrintMarkdown(report, report.RenderMarkdown()); err != nil {
					return err
				}
			default:
				fmt.Fprintf(cmd.OutOrStdout(), "Audit Target: %s | Score: %d/100 (Grade %s) | Total Findings: %d\n\n",
					report.Target, report.Summary.Score, report.Summary.Grade, report.Summary.TotalFindings)
				rows := make([][]string, 0, len(report.Findings))
				for _, f := range report.Findings {
					rows = append(rows, []string{
						strings.ToUpper(string(f.Severity)),
						string(f.Category),
						f.RuleID,
						f.EntityKind + ":" + f.EntityName,
						f.Title,
					})
				}
				if err := p.PrintTable(report, []string{"SEVERITY", "CATEGORY", "RULE_ID", "ENTITY", "TITLE"}, rows); err != nil {
					return err
				}
			}

			if failSev != "" && auditor.HasFindingsAtOrAbove(report.Findings, failSev) {
				return fmt.Errorf("audit failed: found finding(s) at or above severity %q", failSev)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&accountID, "account", "", "GTM Account ID")
	cmd.Flags().StringVar(&containerID, "container", "", "GTM Container ID")
	cmd.Flags().StringVar(&workspaceID, "workspace", "", "GTM Workspace ID (omit when using --live)")
	cmd.Flags().BoolVar(&live, "live", false, "Audit the currently published live container version")
	cmd.Flags().StringVar(&minSeverityRaw, "min-severity", "low", "Minimum severity to report: critical, high, medium, or low")
	cmd.Flags().StringVar(&failOnRaw, "fail-on", "", "Exit non-zero if any finding meets or exceeds severity (critical, high, medium, low)")
	requireFlag(cmd, "account", "container")

	return cmd
}
