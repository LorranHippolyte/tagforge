package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/LorranHippolyte/tagforge/internal/gtm"
	"github.com/spf13/cobra"
	"google.golang.org/api/tagmanager/v2"
)

func prepPrinterAndClient(cmd *cobra.Command, app *appContext) (*Printer, *gtm.Client, error) {
	p, err := app.printer(cmd)
	if err != nil {
		return nil, nil, err
	}
	c, err := app.client(cmd)
	if err != nil {
		return nil, nil, err
	}
	return p, c, nil
}

func bindWorkspaceFlags(cmd *cobra.Command, accountID, containerID, workspaceID *string) {
	cmd.Flags().StringVar(accountID, "account", "", "GTM Account ID")
	cmd.Flags().StringVar(containerID, "container", "", "GTM Container ID")
	cmd.Flags().StringVar(workspaceID, "workspace", "", "GTM Workspace ID")
	requireFlag(cmd, "account", "container", "workspace")
}

func readJSONPayload[T any](filePath string, stdin io.Reader) (*T, error) {
	var data []byte
	var err error
	if strings.TrimSpace(filePath) == "-" {
		data, err = io.ReadAll(io.LimitReader(stdin, 5<<20))
	} else {
		data, err = os.ReadFile(filePath)
	}
	if err != nil {
		return nil, fmt.Errorf("read payload JSON: %w", err)
	}
	var out T
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse payload JSON: %w", err)
	}
	return &out, nil
}

func newTagsCmd(app *appContext) *cobra.Command {
	cmd := &cobra.Command{Use: "tags", Short: "Manage GTM tags in a workspace"}
	var accountID, containerID, workspaceID, tagID, filePath, fingerprint string
	var allowDuplicate bool

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List tags in a workspace",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			items, err := client.ListTags(cmd.Context(), accountID, containerID, workspaceID, gtm.ListOptions{Fresh: app.opts.Fresh})
			if err != nil {
				return err
			}
			rows := make([][]string, 0, len(items))
			for _, t := range items {
				rows = append(rows, []string{t.TagId, t.Name, t.Type, boolStr(t.Paused), orDash(t.Fingerprint)})
			}
			return p.PrintTable(items, []string{"TAG_ID", "NAME", "TYPE", "PAUSED", "FINGERPRINT"}, rows)
		},
	}
	bindWorkspaceFlags(listCmd, &accountID, &containerID, &workspaceID)

	getCmd := &cobra.Command{
		Use:   "get",
		Short: "Get a tag by ID",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			t, err := client.GetTag(cmd.Context(), accountID, containerID, workspaceID, tagID)
			if err != nil {
				return err
			}
			return p.PrintTable(t, []string{"TAG_ID", "NAME", "TYPE", "PAUSED", "FINGERPRINT"}, [][]string{
				{t.TagId, t.Name, t.Type, boolStr(t.Paused), orDash(t.Fingerprint)},
			})
		},
	}
	bindWorkspaceFlags(getCmd, &accountID, &containerID, &workspaceID)
	getCmd.Flags().StringVar(&tagID, "tag", "", "GTM Tag ID")
	requireFlag(getCmd, "tag")

	createCmd := &cobra.Command{
		Use:   "create",
		Short: "Create a tag from a JSON file (or '-' for stdin)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			payload, err := readJSONPayload[tagmanager.Tag](filePath, cmd.InOrStdin())
			if err != nil {
				return err
			}
			t, err := client.CreateTag(cmd.Context(), accountID, containerID, workspaceID, payload, gtm.CreateOptions{AllowDuplicate: allowDuplicate})
			if err != nil {
				return err
			}
			return p.PrintTable(t, []string{"TAG_ID", "NAME", "TYPE", "FINGERPRINT"}, [][]string{
				{t.TagId, t.Name, t.Type, orDash(t.Fingerprint)},
			})
		},
	}
	bindWorkspaceFlags(createCmd, &accountID, &containerID, &workspaceID)
	createCmd.Flags().StringVar(&filePath, "file", "", "Path to tag JSON payload (or '-')")
	createCmd.Flags().BoolVar(&allowDuplicate, "allow-duplicate", false, "Allow duplicate tag name")
	requireFlag(createCmd, "file")

	updateCmd := &cobra.Command{
		Use:   "update",
		Short: "Full-replace update of a tag from JSON (requires --fingerprint)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			payload, err := readJSONPayload[tagmanager.Tag](filePath, cmd.InOrStdin())
			if err != nil {
				return err
			}
			t, err := client.UpdateTag(cmd.Context(), accountID, containerID, workspaceID, tagID, fingerprint, payload)
			if err != nil {
				return err
			}
			return p.PrintTable(t, []string{"TAG_ID", "NAME", "TYPE", "FINGERPRINT"}, [][]string{
				{t.TagId, t.Name, t.Type, orDash(t.Fingerprint)},
			})
		},
	}
	bindWorkspaceFlags(updateCmd, &accountID, &containerID, &workspaceID)
	updateCmd.Flags().StringVar(&tagID, "tag", "", "GTM Tag ID")
	updateCmd.Flags().StringVar(&filePath, "file", "", "Path to full tag JSON payload (or '-')")
	updateCmd.Flags().StringVar(&fingerprint, "fingerprint", "", "Tag fingerprint from 'get'")
	requireFlag(updateCmd, "tag", "file", "fingerprint")

	deleteCmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete a tag (requires --confirm)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			if err := client.DeleteTag(cmd.Context(), accountID, containerID, workspaceID, tagID, app.opts.Confirm); err != nil {
				return err
			}
			return p.PrintMessage(map[string]any{"deleted": true, "tagId": tagID}, fmt.Sprintf("Deleted tag %s.", tagID))
		},
	}
	bindWorkspaceFlags(deleteCmd, &accountID, &containerID, &workspaceID)
	deleteCmd.Flags().StringVar(&tagID, "tag", "", "GTM Tag ID")
	requireFlag(deleteCmd, "tag")

	revertCmd := &cobra.Command{
		Use:   "revert",
		Short: "Revert workspace changes to a tag (requires --fingerprint)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			res, err := client.RevertTag(cmd.Context(), accountID, containerID, workspaceID, tagID, fingerprint)
			if err != nil {
				return err
			}
			return p.PrintMessage(res, fmt.Sprintf("Reverted tag %s.", tagID))
		},
	}
	bindWorkspaceFlags(revertCmd, &accountID, &containerID, &workspaceID)
	revertCmd.Flags().StringVar(&tagID, "tag", "", "GTM Tag ID")
	revertCmd.Flags().StringVar(&fingerprint, "fingerprint", "", "Tag fingerprint from 'get'")
	requireFlag(revertCmd, "tag", "fingerprint")

	cmd.AddCommand(listCmd, getCmd, createCmd, updateCmd, deleteCmd, revertCmd)
	return cmd
}

func newTriggersCmd(app *appContext) *cobra.Command {
	cmd := &cobra.Command{Use: "triggers", Short: "Manage GTM triggers in a workspace"}
	var accountID, containerID, workspaceID, triggerID, filePath, fingerprint string
	var allowDuplicate bool

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List triggers in a workspace",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			items, err := client.ListTriggers(cmd.Context(), accountID, containerID, workspaceID, gtm.ListOptions{Fresh: app.opts.Fresh})
			if err != nil {
				return err
			}
			rows := make([][]string, 0, len(items))
			for _, tr := range items {
				rows = append(rows, []string{tr.TriggerId, tr.Name, tr.Type, orDash(tr.Fingerprint)})
			}
			return p.PrintTable(items, []string{"TRIGGER_ID", "NAME", "TYPE", "FINGERPRINT"}, rows)
		},
	}
	bindWorkspaceFlags(listCmd, &accountID, &containerID, &workspaceID)

	getCmd := &cobra.Command{
		Use:   "get",
		Short: "Get a trigger by ID",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			tr, err := client.GetTrigger(cmd.Context(), accountID, containerID, workspaceID, triggerID)
			if err != nil {
				return err
			}
			return p.PrintTable(tr, []string{"TRIGGER_ID", "NAME", "TYPE", "FINGERPRINT"}, [][]string{
				{tr.TriggerId, tr.Name, tr.Type, orDash(tr.Fingerprint)},
			})
		},
	}
	bindWorkspaceFlags(getCmd, &accountID, &containerID, &workspaceID)
	getCmd.Flags().StringVar(&triggerID, "trigger", "", "GTM Trigger ID")
	requireFlag(getCmd, "trigger")

	createCmd := &cobra.Command{
		Use:   "create",
		Short: "Create a trigger from a JSON file (or '-' for stdin)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			payload, err := readJSONPayload[tagmanager.Trigger](filePath, cmd.InOrStdin())
			if err != nil {
				return err
			}
			tr, err := client.CreateTrigger(cmd.Context(), accountID, containerID, workspaceID, payload, gtm.CreateOptions{AllowDuplicate: allowDuplicate})
			if err != nil {
				return err
			}
			return p.PrintTable(tr, []string{"TRIGGER_ID", "NAME", "TYPE", "FINGERPRINT"}, [][]string{
				{tr.TriggerId, tr.Name, tr.Type, orDash(tr.Fingerprint)},
			})
		},
	}
	bindWorkspaceFlags(createCmd, &accountID, &containerID, &workspaceID)
	createCmd.Flags().StringVar(&filePath, "file", "", "Path to trigger JSON payload (or '-')")
	createCmd.Flags().BoolVar(&allowDuplicate, "allow-duplicate", false, "Allow duplicate trigger name")
	requireFlag(createCmd, "file")

	updateCmd := &cobra.Command{
		Use:   "update",
		Short: "Full-replace update of a trigger from JSON (requires --fingerprint)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			payload, err := readJSONPayload[tagmanager.Trigger](filePath, cmd.InOrStdin())
			if err != nil {
				return err
			}
			tr, err := client.UpdateTrigger(cmd.Context(), accountID, containerID, workspaceID, triggerID, fingerprint, payload)
			if err != nil {
				return err
			}
			return p.PrintTable(tr, []string{"TRIGGER_ID", "NAME", "TYPE", "FINGERPRINT"}, [][]string{
				{tr.TriggerId, tr.Name, tr.Type, orDash(tr.Fingerprint)},
			})
		},
	}
	bindWorkspaceFlags(updateCmd, &accountID, &containerID, &workspaceID)
	updateCmd.Flags().StringVar(&triggerID, "trigger", "", "GTM Trigger ID")
	updateCmd.Flags().StringVar(&filePath, "file", "", "Path to full trigger JSON payload (or '-')")
	updateCmd.Flags().StringVar(&fingerprint, "fingerprint", "", "Trigger fingerprint from 'get'")
	requireFlag(updateCmd, "trigger", "file", "fingerprint")

	deleteCmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete a trigger (requires --confirm)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			if err := client.DeleteTrigger(cmd.Context(), accountID, containerID, workspaceID, triggerID, app.opts.Confirm); err != nil {
				return err
			}
			return p.PrintMessage(map[string]any{"deleted": true, "triggerId": triggerID}, fmt.Sprintf("Deleted trigger %s.", triggerID))
		},
	}
	bindWorkspaceFlags(deleteCmd, &accountID, &containerID, &workspaceID)
	deleteCmd.Flags().StringVar(&triggerID, "trigger", "", "GTM Trigger ID")
	requireFlag(deleteCmd, "trigger")

	revertCmd := &cobra.Command{
		Use:   "revert",
		Short: "Revert workspace changes to a trigger (requires --fingerprint)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			res, err := client.RevertTrigger(cmd.Context(), accountID, containerID, workspaceID, triggerID, fingerprint)
			if err != nil {
				return err
			}
			return p.PrintMessage(res, fmt.Sprintf("Reverted trigger %s.", triggerID))
		},
	}
	bindWorkspaceFlags(revertCmd, &accountID, &containerID, &workspaceID)
	revertCmd.Flags().StringVar(&triggerID, "trigger", "", "GTM Trigger ID")
	revertCmd.Flags().StringVar(&fingerprint, "fingerprint", "", "Trigger fingerprint from 'get'")
	requireFlag(revertCmd, "trigger", "fingerprint")

	cmd.AddCommand(listCmd, getCmd, createCmd, updateCmd, deleteCmd, revertCmd)
	return cmd
}

func newVariablesCmd(app *appContext) *cobra.Command {
	cmd := &cobra.Command{Use: "variables", Short: "Manage GTM user-defined variables in a workspace"}
	var accountID, containerID, workspaceID, variableID, filePath, fingerprint string
	var allowDuplicate bool

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List variables in a workspace",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			items, err := client.ListVariables(cmd.Context(), accountID, containerID, workspaceID, gtm.ListOptions{Fresh: app.opts.Fresh})
			if err != nil {
				return err
			}
			rows := make([][]string, 0, len(items))
			for _, v := range items {
				rows = append(rows, []string{v.VariableId, v.Name, v.Type, orDash(v.Fingerprint)})
			}
			return p.PrintTable(items, []string{"VARIABLE_ID", "NAME", "TYPE", "FINGERPRINT"}, rows)
		},
	}
	bindWorkspaceFlags(listCmd, &accountID, &containerID, &workspaceID)

	getCmd := &cobra.Command{
		Use:   "get",
		Short: "Get a variable by ID",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			v, err := client.GetVariable(cmd.Context(), accountID, containerID, workspaceID, variableID)
			if err != nil {
				return err
			}
			return p.PrintTable(v, []string{"VARIABLE_ID", "NAME", "TYPE", "FINGERPRINT"}, [][]string{
				{v.VariableId, v.Name, v.Type, orDash(v.Fingerprint)},
			})
		},
	}
	bindWorkspaceFlags(getCmd, &accountID, &containerID, &workspaceID)
	getCmd.Flags().StringVar(&variableID, "variable", "", "GTM Variable ID")
	requireFlag(getCmd, "variable")

	createCmd := &cobra.Command{
		Use:   "create",
		Short: "Create a variable from a JSON file (or '-' for stdin)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			payload, err := readJSONPayload[tagmanager.Variable](filePath, cmd.InOrStdin())
			if err != nil {
				return err
			}
			v, err := client.CreateVariable(cmd.Context(), accountID, containerID, workspaceID, payload, gtm.CreateOptions{AllowDuplicate: allowDuplicate})
			if err != nil {
				return err
			}
			return p.PrintTable(v, []string{"VARIABLE_ID", "NAME", "TYPE", "FINGERPRINT"}, [][]string{
				{v.VariableId, v.Name, v.Type, orDash(v.Fingerprint)},
			})
		},
	}
	bindWorkspaceFlags(createCmd, &accountID, &containerID, &workspaceID)
	createCmd.Flags().StringVar(&filePath, "file", "", "Path to variable JSON payload (or '-')")
	createCmd.Flags().BoolVar(&allowDuplicate, "allow-duplicate", false, "Allow duplicate variable name")
	requireFlag(createCmd, "file")

	updateCmd := &cobra.Command{
		Use:   "update",
		Short: "Full-replace update of a variable from JSON (requires --fingerprint)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			payload, err := readJSONPayload[tagmanager.Variable](filePath, cmd.InOrStdin())
			if err != nil {
				return err
			}
			v, err := client.UpdateVariable(cmd.Context(), accountID, containerID, workspaceID, variableID, fingerprint, payload)
			if err != nil {
				return err
			}
			return p.PrintTable(v, []string{"VARIABLE_ID", "NAME", "TYPE", "FINGERPRINT"}, [][]string{
				{v.VariableId, v.Name, v.Type, orDash(v.Fingerprint)},
			})
		},
	}
	bindWorkspaceFlags(updateCmd, &accountID, &containerID, &workspaceID)
	updateCmd.Flags().StringVar(&variableID, "variable", "", "GTM Variable ID")
	updateCmd.Flags().StringVar(&filePath, "file", "", "Path to full variable JSON payload (or '-')")
	updateCmd.Flags().StringVar(&fingerprint, "fingerprint", "", "Variable fingerprint from 'get'")
	requireFlag(updateCmd, "variable", "file", "fingerprint")

	deleteCmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete a variable (requires --confirm)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			if err := client.DeleteVariable(cmd.Context(), accountID, containerID, workspaceID, variableID, app.opts.Confirm); err != nil {
				return err
			}
			return p.PrintMessage(map[string]any{"deleted": true, "variableId": variableID}, fmt.Sprintf("Deleted variable %s.", variableID))
		},
	}
	bindWorkspaceFlags(deleteCmd, &accountID, &containerID, &workspaceID)
	deleteCmd.Flags().StringVar(&variableID, "variable", "", "GTM Variable ID")
	requireFlag(deleteCmd, "variable")

	revertCmd := &cobra.Command{
		Use:   "revert",
		Short: "Revert workspace changes to a variable (requires --fingerprint)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			res, err := client.RevertVariable(cmd.Context(), accountID, containerID, workspaceID, variableID, fingerprint)
			if err != nil {
				return err
			}
			return p.PrintMessage(res, fmt.Sprintf("Reverted variable %s.", variableID))
		},
	}
	bindWorkspaceFlags(revertCmd, &accountID, &containerID, &workspaceID)
	revertCmd.Flags().StringVar(&variableID, "variable", "", "GTM Variable ID")
	revertCmd.Flags().StringVar(&fingerprint, "fingerprint", "", "Variable fingerprint from 'get'")
	requireFlag(revertCmd, "variable", "fingerprint")

	cmd.AddCommand(listCmd, getCmd, createCmd, updateCmd, deleteCmd, revertCmd)
	return cmd
}

func newBuiltInVariablesCmd(app *appContext) *cobra.Command {
	cmd := &cobra.Command{Use: "builtin-variables", Short: "Manage enabled GTM built-in variables"}
	var accountID, containerID, workspaceID string
	var types []string

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List enabled built-in variables",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			items, err := client.ListBuiltInVariables(cmd.Context(), accountID, containerID, workspaceID, gtm.ListOptions{Fresh: app.opts.Fresh})
			if err != nil {
				return err
			}
			rows := make([][]string, 0, len(items))
			for _, bv := range items {
				rows = append(rows, []string{bv.Type, bv.Name})
			}
			return p.PrintTable(items, []string{"TYPE", "NAME"}, rows)
		},
	}
	bindWorkspaceFlags(listCmd, &accountID, &containerID, &workspaceID)

	enableCmd := &cobra.Command{
		Use:   "enable",
		Short: "Enable built-in variable types (e.g. --type pageUrl --type clickUrl)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			items, err := client.EnableBuiltInVariables(cmd.Context(), accountID, containerID, workspaceID, types)
			if err != nil {
				return err
			}
			rows := make([][]string, 0, len(items))
			for _, bv := range items {
				rows = append(rows, []string{bv.Type, bv.Name})
			}
			return p.PrintTable(items, []string{"TYPE", "NAME"}, rows)
		},
	}
	bindWorkspaceFlags(enableCmd, &accountID, &containerID, &workspaceID)
	enableCmd.Flags().StringSliceVar(&types, "type", nil, "Built-in variable type(s) to enable")
	requireFlag(enableCmd, "type")

	disableCmd := &cobra.Command{
		Use:   "disable",
		Short: "Disable built-in variable types (requires --confirm)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			if err := client.DisableBuiltInVariables(cmd.Context(), accountID, containerID, workspaceID, types, app.opts.Confirm); err != nil {
				return err
			}
			return p.PrintMessage(map[string]any{"disabled": types}, fmt.Sprintf("Disabled built-in variables: %s.", strings.Join(types, ", ")))
		},
	}
	bindWorkspaceFlags(disableCmd, &accountID, &containerID, &workspaceID)
	disableCmd.Flags().StringSliceVar(&types, "type", nil, "Built-in variable type(s) to disable")
	requireFlag(disableCmd, "type")

	cmd.AddCommand(listCmd, enableCmd, disableCmd)
	return cmd
}

func newFoldersCmd(app *appContext) *cobra.Command {
	cmd := &cobra.Command{Use: "folders", Short: "Manage GTM folders in a workspace"}
	var accountID, containerID, workspaceID, folderID, name, notes, fingerprint string
	var allowDuplicate bool

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List folders in a workspace",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			items, err := client.ListFolders(cmd.Context(), accountID, containerID, workspaceID, gtm.ListOptions{Fresh: app.opts.Fresh})
			if err != nil {
				return err
			}
			rows := make([][]string, 0, len(items))
			for _, f := range items {
				rows = append(rows, []string{f.FolderId, f.Name, orDash(f.Fingerprint)})
			}
			return p.PrintTable(items, []string{"FOLDER_ID", "NAME", "FINGERPRINT"}, rows)
		},
	}
	bindWorkspaceFlags(listCmd, &accountID, &containerID, &workspaceID)

	getCmd := &cobra.Command{
		Use:   "get",
		Short: "Get a folder by ID",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			f, err := client.GetFolder(cmd.Context(), accountID, containerID, workspaceID, folderID)
			if err != nil {
				return err
			}
			return p.PrintTable(f, []string{"FOLDER_ID", "NAME", "FINGERPRINT"}, [][]string{
				{f.FolderId, f.Name, orDash(f.Fingerprint)},
			})
		},
	}
	bindWorkspaceFlags(getCmd, &accountID, &containerID, &workspaceID)
	getCmd.Flags().StringVar(&folderID, "folder", "", "GTM Folder ID")
	requireFlag(getCmd, "folder")

	createCmd := &cobra.Command{
		Use:   "create",
		Short: "Create a folder",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			f, err := client.CreateFolder(cmd.Context(), accountID, containerID, workspaceID, &tagmanager.Folder{
				Name:  name,
				Notes: notes,
			}, gtm.CreateOptions{AllowDuplicate: allowDuplicate})
			if err != nil {
				return err
			}
			return p.PrintTable(f, []string{"FOLDER_ID", "NAME", "FINGERPRINT"}, [][]string{
				{f.FolderId, f.Name, orDash(f.Fingerprint)},
			})
		},
	}
	bindWorkspaceFlags(createCmd, &accountID, &containerID, &workspaceID)
	createCmd.Flags().StringVar(&name, "name", "", "Folder name")
	createCmd.Flags().StringVar(&notes, "notes", "", "Folder notes")
	createCmd.Flags().BoolVar(&allowDuplicate, "allow-duplicate", false, "Allow duplicate folder name")
	requireFlag(createCmd, "name")

	updateCmd := &cobra.Command{
		Use:   "update",
		Short: "Update a folder (requires --fingerprint)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			f, err := client.UpdateFolder(cmd.Context(), accountID, containerID, workspaceID, folderID, fingerprint, &tagmanager.Folder{
				Name:  name,
				Notes: notes,
			})
			if err != nil {
				return err
			}
			return p.PrintTable(f, []string{"FOLDER_ID", "NAME", "FINGERPRINT"}, [][]string{
				{f.FolderId, f.Name, orDash(f.Fingerprint)},
			})
		},
	}
	bindWorkspaceFlags(updateCmd, &accountID, &containerID, &workspaceID)
	updateCmd.Flags().StringVar(&folderID, "folder", "", "GTM Folder ID")
	updateCmd.Flags().StringVar(&name, "name", "", "Folder name")
	updateCmd.Flags().StringVar(&notes, "notes", "", "Folder notes")
	updateCmd.Flags().StringVar(&fingerprint, "fingerprint", "", "Folder fingerprint from 'get'")
	requireFlag(updateCmd, "folder", "name", "fingerprint")

	deleteCmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete a folder (requires --confirm)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			if err := client.DeleteFolder(cmd.Context(), accountID, containerID, workspaceID, folderID, app.opts.Confirm); err != nil {
				return err
			}
			return p.PrintMessage(map[string]any{"deleted": true, "folderId": folderID}, fmt.Sprintf("Deleted folder %s.", folderID))
		},
	}
	bindWorkspaceFlags(deleteCmd, &accountID, &containerID, &workspaceID)
	deleteCmd.Flags().StringVar(&folderID, "folder", "", "GTM Folder ID")
	requireFlag(deleteCmd, "folder")

	entitiesCmd := &cobra.Command{
		Use:   "entities",
		Short: "List tags, triggers, and variables inside a folder",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			fe, err := client.ListFolderEntities(cmd.Context(), accountID, containerID, workspaceID, folderID)
			if err != nil {
				return err
			}
			rows := make([][]string, 0, len(fe.Tag)+len(fe.Trigger)+len(fe.Variable))
			for _, t := range fe.Tag {
				rows = append(rows, []string{"tag", t.TagId, t.Name, t.Type})
			}
			for _, tr := range fe.Trigger {
				rows = append(rows, []string{"trigger", tr.TriggerId, tr.Name, tr.Type})
			}
			for _, v := range fe.Variable {
				rows = append(rows, []string{"variable", v.VariableId, v.Name, v.Type})
			}
			return p.PrintTable(fe, []string{"KIND", "ID", "NAME", "TYPE"}, rows)
		},
	}
	bindWorkspaceFlags(entitiesCmd, &accountID, &containerID, &workspaceID)
	entitiesCmd.Flags().StringVar(&folderID, "folder", "", "GTM Folder ID")
	requireFlag(entitiesCmd, "folder")

	cmd.AddCommand(listCmd, getCmd, createCmd, updateCmd, deleteCmd, entitiesCmd)
	return cmd
}

func newTemplatesCmd(app *appContext) *cobra.Command {
	cmd := &cobra.Command{Use: "templates", Short: "Inspect custom templates in a workspace"}
	var accountID, containerID, workspaceID, templateID string

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List custom templates in a workspace",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			items, err := client.ListTemplates(cmd.Context(), accountID, containerID, workspaceID, gtm.ListOptions{Fresh: app.opts.Fresh})
			if err != nil {
				return err
			}
			rows := make([][]string, 0, len(items))
			for _, tpl := range items {
				galleryID := "-"
				if tpl.GalleryReference != nil && tpl.GalleryReference.GalleryTemplateId != "" {
					galleryID = tpl.GalleryReference.GalleryTemplateId
				}
				rows = append(rows, []string{tpl.TemplateId, tpl.Name, galleryID, orDash(tpl.Fingerprint)})
			}
			return p.PrintTable(items, []string{"TEMPLATE_ID", "NAME", "GALLERY_ID", "FINGERPRINT"}, rows)
		},
	}
	bindWorkspaceFlags(listCmd, &accountID, &containerID, &workspaceID)

	getCmd := &cobra.Command{
		Use:   "get",
		Short: "Get a custom template by ID",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			tpl, err := client.GetTemplate(cmd.Context(), accountID, containerID, workspaceID, templateID)
			if err != nil {
				return err
			}
			return p.PrintTable(tpl, []string{"TEMPLATE_ID", "NAME", "FINGERPRINT"}, [][]string{
				{tpl.TemplateId, tpl.Name, orDash(tpl.Fingerprint)},
			})
		},
	}
	bindWorkspaceFlags(getCmd, &accountID, &containerID, &workspaceID)
	getCmd.Flags().StringVar(&templateID, "template", "", "GTM Custom Template ID")
	requireFlag(getCmd, "template")

	cmd.AddCommand(listCmd, getCmd)
	return cmd
}

func newClientsCmd(app *appContext) *cobra.Command {
	cmd := &cobra.Command{Use: "clients", Short: "Inspect server-side GTM clients in a workspace"}
	var accountID, containerID, workspaceID, clientID string

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List sGTM clients in a workspace",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			items, err := client.ListClients(cmd.Context(), accountID, containerID, workspaceID, gtm.ListOptions{Fresh: app.opts.Fresh})
			if err != nil {
				return err
			}
			rows := make([][]string, 0, len(items))
			for _, cl := range items {
				rows = append(rows, []string{cl.ClientId, cl.Name, cl.Type, fmt.Sprintf("%d", cl.Priority), orDash(cl.Fingerprint)})
			}
			return p.PrintTable(items, []string{"CLIENT_ID", "NAME", "TYPE", "PRIORITY", "FINGERPRINT"}, rows)
		},
	}
	bindWorkspaceFlags(listCmd, &accountID, &containerID, &workspaceID)

	getCmd := &cobra.Command{
		Use:   "get",
		Short: "Get an sGTM client by ID",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			cl, err := client.GetClient(cmd.Context(), accountID, containerID, workspaceID, clientID)
			if err != nil {
				return err
			}
			return p.PrintTable(cl, []string{"CLIENT_ID", "NAME", "TYPE", "PRIORITY", "FINGERPRINT"}, [][]string{
				{cl.ClientId, cl.Name, cl.Type, fmt.Sprintf("%d", cl.Priority), orDash(cl.Fingerprint)},
			})
		},
	}
	bindWorkspaceFlags(getCmd, &accountID, &containerID, &workspaceID)
	getCmd.Flags().StringVar(&clientID, "client", "", "sGTM Client ID")
	requireFlag(getCmd, "client")

	cmd.AddCommand(listCmd, getCmd)
	return cmd
}

func newTransformationsCmd(app *appContext) *cobra.Command {
	cmd := &cobra.Command{Use: "transformations", Short: "Inspect server-side GTM transformations in a workspace"}
	var accountID, containerID, workspaceID, transformationID string

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List sGTM transformations in a workspace",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			items, err := client.ListTransformations(cmd.Context(), accountID, containerID, workspaceID, gtm.ListOptions{Fresh: app.opts.Fresh})
			if err != nil {
				return err
			}
			rows := make([][]string, 0, len(items))
			for _, tr := range items {
				rows = append(rows, []string{tr.TransformationId, tr.Name, tr.Type, orDash(tr.Fingerprint)})
			}
			return p.PrintTable(items, []string{"TRANSFORMATION_ID", "NAME", "TYPE", "FINGERPRINT"}, rows)
		},
	}
	bindWorkspaceFlags(listCmd, &accountID, &containerID, &workspaceID)

	getCmd := &cobra.Command{
		Use:   "get",
		Short: "Get an sGTM transformation by ID",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			tr, err := client.GetTransformation(cmd.Context(), accountID, containerID, workspaceID, transformationID)
			if err != nil {
				return err
			}
			return p.PrintTable(tr, []string{"TRANSFORMATION_ID", "NAME", "TYPE", "FINGERPRINT"}, [][]string{
				{tr.TransformationId, tr.Name, tr.Type, orDash(tr.Fingerprint)},
			})
		},
	}
	bindWorkspaceFlags(getCmd, &accountID, &containerID, &workspaceID)
	getCmd.Flags().StringVar(&transformationID, "transformation", "", "sGTM Transformation ID")
	requireFlag(getCmd, "transformation")

	cmd.AddCommand(listCmd, getCmd)
	return cmd
}
