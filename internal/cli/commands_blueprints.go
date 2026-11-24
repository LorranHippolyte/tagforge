package cli

import (
	"fmt"
	"strings"

	"github.com/LorranHippolyte/tagforge/internal/blueprints"
	"github.com/spf13/cobra"
)

func newBlueprintsCmd(app *appContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "blueprints",
		Short: "Inspect, plan, and apply official declarative GTM tracking blueprints",
	}

	reg, regErr := blueprints.NewDefaultRegistry()

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List available official GTM blueprints",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if regErr != nil {
				return regErr
			}
			p, err := app.printer(cmd)
			if err != nil {
				return err
			}
			specs := reg.List()
			rows := make([][]string, 0, len(specs))
			for _, s := range specs {
				rows = append(rows, []string{s.ID, s.Context, s.Category, s.Name})
			}
			return p.PrintTable(specs, []string{"ID", "CONTEXT", "CATEGORY", "NAME"}, rows)
		},
	}

	var name string
	showCmd := &cobra.Command{
		Use:   "show",
		Short: "Show blueprint parameters, entities, docs URLs, and dataLayer contract",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if regErr != nil {
				return regErr
			}
			p, err := app.printer(cmd)
			if err != nil {
				return err
			}
			spec, ok := reg.Get(name)
			if !ok {
				return fmt.Errorf("unknown blueprint %q (run 'tagforge blueprints list')", name)
			}
			if p.Format == FormatJSON {
				return p.PrintJSON(spec)
			}
			rows := [][]string{
				{"ID", spec.ID},
				{"Name", spec.Name},
				{"Context", spec.Context},
				{"Category", spec.Category},
				{"Built-ins", strings.Join(spec.BuiltInVariables, ", ")},
			}
			for _, param := range spec.Params {
				reqLabel := "optional"
				if param.Required {
					reqLabel = "required"
				}
				rows = append(rows, []string{"Param: " + param.Name, fmt.Sprintf("[%s] %s", reqLabel, param.Description)})
			}
			return p.PrintTable(spec, []string{"FIELD", "VALUE"}, rows)
		},
	}
	showCmd.Flags().StringVar(&name, "name", "", "Blueprint ID")
	requireFlag(showCmd, "name")

	var accountID, containerID, workspaceID string
	var rawParams []string
	planCmd := &cobra.Command{
		Use:   "plan",
		Short: "Dry-run a blueprint against a GTM workspace (no mutations)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if regErr != nil {
				return regErr
			}
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			params, err := parseKeyValueParams(rawParams)
			if err != nil {
				return err
			}
			res, err := blueprints.Plan(cmd.Context(), client, reg, blueprints.PlanInput{
				AccountID:   accountID,
				ContainerID: containerID,
				WorkspaceID: workspaceID,
				BlueprintID: name,
				Params:      params,
			})
			if err != nil {
				return err
			}
			rows := make([][]string, 0, len(res.Actions))
			for _, act := range res.Actions {
				rows = append(rows, []string{act.Kind, act.EntityType, act.Name, orDash(act.TempID), orDash(act.Details)})
			}
			return p.PrintTable(res, []string{"ACTION", "ENTITY", "NAME", "TEMP_ID", "DETAILS"}, rows)
		},
	}
	planCmd.Flags().StringVar(&name, "name", "", "Blueprint ID")
	planCmd.Flags().StringVar(&accountID, "account", "", "GTM Account ID")
	planCmd.Flags().StringVar(&containerID, "container", "", "GTM Container ID")
	planCmd.Flags().StringVar(&workspaceID, "workspace", "", "GTM Workspace ID")
	planCmd.Flags().StringArrayVar(&rawParams, "param", nil, "Blueprint parameter in key=value form (repeatable)")
	requireFlag(planCmd, "name", "account", "container", "workspace")

	var createWorkspace string
	var importTemplates, ackPerms bool
	applyCmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply an official blueprint atomically to a GTM workspace",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if regErr != nil {
				return regErr
			}
			if strings.TrimSpace(workspaceID) == "" && strings.TrimSpace(createWorkspace) == "" {
				return fmt.Errorf("either --workspace or --create-workspace must be provided")
			}
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			params, err := parseKeyValueParams(rawParams)
			if err != nil {
				return err
			}
			res, err := blueprints.Apply(cmd.Context(), client, reg, blueprints.ApplyInput{
				PlanInput: blueprints.PlanInput{
					AccountID:   accountID,
					ContainerID: containerID,
					WorkspaceID: workspaceID,
					BlueprintID: name,
					Params:      params,
				},
				CreateWorkspaceName:            createWorkspace,
				ImportGalleryTemplates:         importTemplates,
				AcknowledgeTemplatePermissions: ackPerms,
			})
			if err != nil {
				return err
			}
			return p.PrintTable(res, []string{"BLUEPRINT", "WORKSPACE_ID", "IMPORTED_TEMPLATES", "ENABLED_BUILTINS", "APPLIED", "SKIPPED"}, [][]string{
				{
					res.BlueprintID,
					res.WorkspaceID,
					fmt.Sprintf("%d", len(res.ImportedTemplates)),
					fmt.Sprintf("%d", len(res.EnabledBuiltIns)),
					fmt.Sprintf("%d", res.AppliedChanges),
					fmt.Sprintf("%d", res.SkippedExisting),
				},
			})
		},
	}
	applyCmd.Flags().StringVar(&name, "name", "", "Blueprint ID")
	applyCmd.Flags().StringVar(&accountID, "account", "", "GTM Account ID")
	applyCmd.Flags().StringVar(&containerID, "container", "", "GTM Container ID")
	applyCmd.Flags().StringVar(&workspaceID, "workspace", "", "Target GTM Workspace ID (optional if --create-workspace is set)")
	applyCmd.Flags().StringArrayVar(&rawParams, "param", nil, "Blueprint parameter in key=value form (repeatable)")
	applyCmd.Flags().StringVar(&createWorkspace, "create-workspace", "", "Create a dedicated workspace with this name before applying")
	applyCmd.Flags().BoolVar(&importTemplates, "import-templates", false, "Automatically import missing Community Template Gallery templates")
	applyCmd.Flags().BoolVar(&ackPerms, "acknowledge-permissions", true, "Acknowledge permissions when importing Community Gallery templates")
	requireFlag(applyCmd, "name", "account", "container")

	cmd.AddCommand(listCmd, showCmd, planCmd, applyCmd)
	return cmd
}

func parseKeyValueParams(entries []string) (map[string]string, error) {
	out := make(map[string]string, len(entries))
	for _, entry := range entries {
		k, v, ok := strings.Cut(entry, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("invalid --param %q: expected key=value", entry)
		}
		out[strings.TrimSpace(k)] = v
	}
	return out, nil
}
