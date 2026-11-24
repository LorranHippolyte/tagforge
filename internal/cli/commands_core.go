package cli

import (
	"fmt"
	"strings"

	"github.com/LorranHippolyte/tagforge/internal/gtm"
	"github.com/spf13/cobra"
	"google.golang.org/api/tagmanager/v2"
)

func newAccountsCmd(app *appContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "accounts",
		Short: "Inspect Google Tag Manager accounts",
	}

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List accessible GTM accounts",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			items, err := client.ListAccounts(cmd.Context(), gtm.ListOptions{Fresh: app.opts.Fresh})
			if err != nil {
				return err
			}
			rows := make([][]string, 0, len(items))
			for _, a := range items {
				rows = append(rows, []string{a.AccountId, a.Name, boolStr(a.ShareData), orDash(a.Fingerprint)})
			}
			return p.PrintTable(items, []string{"ACCOUNT_ID", "NAME", "SHARE_DATA", "FINGERPRINT"}, rows)
		},
	}

	var accountID string
	getCmd := &cobra.Command{
		Use:   "get",
		Short: "Get a GTM account by ID",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			item, err := client.GetAccount(cmd.Context(), accountID)
			if err != nil {
				return err
			}
			return p.PrintTable(item, []string{"ACCOUNT_ID", "NAME", "SHARE_DATA", "FINGERPRINT"}, [][]string{
				{item.AccountId, item.Name, boolStr(item.ShareData), orDash(item.Fingerprint)},
			})
		},
	}
	getCmd.Flags().StringVar(&accountID, "account", "", "GTM Account ID")
	requireFlag(getCmd, "account")

	cmd.AddCommand(listCmd, getCmd)
	return cmd
}

func newContainersCmd(app *appContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "containers",
		Short: "Inspect Google Tag Manager containers",
	}

	var accountID, containerID, publicID string

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List containers in a GTM account",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			items, err := client.ListContainers(cmd.Context(), accountID, gtm.ListOptions{Fresh: app.opts.Fresh})
			if err != nil {
				return err
			}
			rows := make([][]string, 0, len(items))
			for _, c := range items {
				rows = append(rows, []string{c.ContainerId, c.PublicId, c.Name, strings.Join(c.UsageContext, ","), orDash(c.Fingerprint)})
			}
			return p.PrintTable(items, []string{"CONTAINER_ID", "PUBLIC_ID", "NAME", "USAGE_CONTEXT", "FINGERPRINT"}, rows)
		},
	}
	listCmd.Flags().StringVar(&accountID, "account", "", "GTM Account ID")
	requireFlag(listCmd, "account")

	getCmd := &cobra.Command{
		Use:   "get",
		Short: "Get a GTM container by ID",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			c, err := client.GetContainer(cmd.Context(), accountID, containerID)
			if err != nil {
				return err
			}
			return p.PrintTable(c, []string{"CONTAINER_ID", "PUBLIC_ID", "NAME", "USAGE_CONTEXT", "FINGERPRINT"}, [][]string{
				{c.ContainerId, c.PublicId, c.Name, strings.Join(c.UsageContext, ","), orDash(c.Fingerprint)},
			})
		},
	}
	getCmd.Flags().StringVar(&accountID, "account", "", "GTM Account ID")
	getCmd.Flags().StringVar(&containerID, "container", "", "GTM Container ID")
	requireFlag(getCmd, "account", "container")

	lookupCmd := &cobra.Command{
		Use:   "lookup",
		Short: "Look up a GTM container by its public ID (e.g. GTM-XXXXXXX)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			c, err := client.LookupContainer(cmd.Context(), publicID)
			if err != nil {
				return err
			}
			return p.PrintTable(c, []string{"ACCOUNT_ID", "CONTAINER_ID", "PUBLIC_ID", "NAME", "USAGE_CONTEXT"}, [][]string{
				{c.AccountId, c.ContainerId, c.PublicId, c.Name, strings.Join(c.UsageContext, ",")},
			})
		},
	}
	lookupCmd.Flags().StringVar(&publicID, "public-id", "", "GTM public container ID (e.g. GTM-XXXXXXX)")
	requireFlag(lookupCmd, "public-id")

	snippetCmd := &cobra.Command{
		Use:   "snippet",
		Short: "Get installation snippet for a GTM container",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			resp, err := client.GetContainerSnippet(cmd.Context(), accountID, containerID)
			if err != nil {
				return err
			}
			return p.PrintMessage(resp, resp.Snippet)
		},
	}
	snippetCmd.Flags().StringVar(&accountID, "account", "", "GTM Account ID")
	snippetCmd.Flags().StringVar(&containerID, "container", "", "GTM Container ID")
	requireFlag(snippetCmd, "account", "container")

	cmd.AddCommand(listCmd, getCmd, lookupCmd, snippetCmd)
	return cmd
}

func newWorkspacesCmd(app *appContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workspaces",
		Short: "Manage GTM workspaces, status, sync, previews, and version creation",
	}

	var accountID, containerID, workspaceID, name, description, notes string
	var allowDuplicate bool

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List workspaces in a GTM container",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			items, err := client.ListWorkspaces(cmd.Context(), accountID, containerID, gtm.ListOptions{Fresh: app.opts.Fresh})
			if err != nil {
				return err
			}
			rows := make([][]string, 0, len(items))
			for _, w := range items {
				rows = append(rows, []string{w.WorkspaceId, w.Name, orDash(w.Description), orDash(w.Fingerprint)})
			}
			return p.PrintTable(items, []string{"WORKSPACE_ID", "NAME", "DESCRIPTION", "FINGERPRINT"}, rows)
		},
	}
	listCmd.Flags().StringVar(&accountID, "account", "", "GTM Account ID")
	listCmd.Flags().StringVar(&containerID, "container", "", "GTM Container ID")
	requireFlag(listCmd, "account", "container")

	getCmd := &cobra.Command{
		Use:   "get",
		Short: "Get a GTM workspace",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			w, err := client.GetWorkspace(cmd.Context(), accountID, containerID, workspaceID)
			if err != nil {
				return err
			}
			return p.PrintTable(w, []string{"WORKSPACE_ID", "NAME", "DESCRIPTION", "FINGERPRINT"}, [][]string{
				{w.WorkspaceId, w.Name, orDash(w.Description), orDash(w.Fingerprint)},
			})
		},
	}
	bindWorkspaceFlags(getCmd, &accountID, &containerID, &workspaceID)

	createCmd := &cobra.Command{
		Use:   "create",
		Short: "Create a dedicated GTM workspace",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			w, err := client.CreateWorkspace(cmd.Context(), accountID, containerID, &tagmanager.Workspace{
				Name:        name,
				Description: description,
			}, gtm.CreateOptions{AllowDuplicate: allowDuplicate})
			if err != nil {
				return err
			}
			return p.PrintTable(w, []string{"WORKSPACE_ID", "NAME", "FINGERPRINT"}, [][]string{
				{w.WorkspaceId, w.Name, orDash(w.Fingerprint)},
			})
		},
	}
	createCmd.Flags().StringVar(&accountID, "account", "", "GTM Account ID")
	createCmd.Flags().StringVar(&containerID, "container", "", "GTM Container ID")
	createCmd.Flags().StringVar(&name, "name", "", "Workspace display name")
	createCmd.Flags().StringVar(&description, "description", "", "Workspace description")
	createCmd.Flags().BoolVar(&allowDuplicate, "allow-duplicate", false, "Allow duplicate workspace name")
	requireFlag(createCmd, "account", "container", "name")

	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Inspect pending changes and merge conflicts in a workspace",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			st, err := client.GetWorkspaceStatus(cmd.Context(), accountID, containerID, workspaceID)
			if err != nil {
				return err
			}
			return p.PrintTable(st, []string{"CHANGES", "MERGE_CONFLICTS"}, [][]string{
				{fmt.Sprintf("%d", len(st.WorkspaceChange)), fmt.Sprintf("%d", len(st.MergeConflict))},
			})
		},
	}
	bindWorkspaceFlags(statusCmd, &accountID, &containerID, &workspaceID)

	syncCmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync workspace with the latest container version",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			res, err := client.SyncWorkspace(cmd.Context(), accountID, containerID, workspaceID)
			if err != nil {
				return err
			}
			syncErr := false
			if res.SyncStatus != nil {
				syncErr = res.SyncStatus.SyncError
			}
			return p.PrintTable(res, []string{"SYNC_ERROR", "MERGE_CONFLICTS"}, [][]string{
				{boolStr(syncErr), fmt.Sprintf("%d", len(res.MergeConflict))},
			})
		},
	}
	bindWorkspaceFlags(syncCmd, &accountID, &containerID, &workspaceID)

	previewCmd := &cobra.Command{
		Use:   "preview",
		Short: "Compile workspace without publishing (quick preview)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			res, err := client.QuickPreviewWorkspace(cmd.Context(), accountID, containerID, workspaceID)
			if err != nil {
				return err
			}
			return p.PrintTable(res, []string{"COMPILER_ERROR"}, [][]string{
				{boolStr(res.CompilerError)},
			})
		},
	}
	bindWorkspaceFlags(previewCmd, &accountID, &containerID, &workspaceID)

	createVersionCmd := &cobra.Command{
		Use:   "create-version",
		Short: "Create a container version from workspace (requires pending changes and zero conflicts)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			res, err := client.CreateVersion(cmd.Context(), accountID, containerID, workspaceID, gtm.CreateVersionOptions{
				Name:  name,
				Notes: notes,
			})
			if err != nil {
				return err
			}
			verID, verName, fp := "-", "-", "-"
			if res.ContainerVersion != nil {
				verID = res.ContainerVersion.ContainerVersionId
				verName = res.ContainerVersion.Name
				fp = res.ContainerVersion.Fingerprint
			}
			return p.PrintTable(res, []string{"VERSION_ID", "NAME", "COMPILER_ERROR", "FINGERPRINT"}, [][]string{
				{verID, verName, boolStr(res.CompilerError), orDash(fp)},
			})
		},
	}
	bindWorkspaceFlags(createVersionCmd, &accountID, &containerID, &workspaceID)
	createVersionCmd.Flags().StringVar(&name, "name", "", "Container version name")
	createVersionCmd.Flags().StringVar(&notes, "notes", "", "Container version release notes")
	requireFlag(createVersionCmd, "name")

	cmd.AddCommand(listCmd, getCmd, createCmd, statusCmd, syncCmd, previewCmd, createVersionCmd)
	return cmd
}

func newVersionsCmd(app *appContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "versions",
		Short: "Inspect and publish GTM container versions",
	}

	var accountID, containerID, versionID, fingerprint string
	var includeDeleted bool

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List container version headers",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			items, err := client.ListVersionHeaders(cmd.Context(), accountID, containerID, includeDeleted, gtm.ListOptions{Fresh: app.opts.Fresh})
			if err != nil {
				return err
			}
			rows := make([][]string, 0, len(items))
			for _, v := range items {
				rows = append(rows, []string{v.ContainerVersionId, orDash(v.Name), orDash(v.NumTags), orDash(v.NumTriggers), orDash(v.NumVariables), boolStr(v.Deleted)})
			}
			return p.PrintTable(items, []string{"VERSION_ID", "NAME", "TAGS", "TRIGGERS", "VARIABLES", "DELETED"}, rows)
		},
	}
	listCmd.Flags().StringVar(&accountID, "account", "", "GTM Account ID")
	listCmd.Flags().StringVar(&containerID, "container", "", "GTM Container ID")
	listCmd.Flags().BoolVar(&includeDeleted, "include-deleted", false, "Include deleted container versions")
	requireFlag(listCmd, "account", "container")

	liveCmd := &cobra.Command{
		Use:   "live",
		Short: "Get the currently published live container version",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			v, err := client.GetLiveVersion(cmd.Context(), accountID, containerID)
			if err != nil {
				return err
			}
			return p.PrintTable(v, []string{"VERSION_ID", "NAME", "TAGS", "TRIGGERS", "VARIABLES", "FINGERPRINT"}, [][]string{
				{v.ContainerVersionId, orDash(v.Name), fmt.Sprintf("%d", len(v.Tag)), fmt.Sprintf("%d", len(v.Trigger)), fmt.Sprintf("%d", len(v.Variable)), orDash(v.Fingerprint)},
			})
		},
	}
	liveCmd.Flags().StringVar(&accountID, "account", "", "GTM Account ID")
	liveCmd.Flags().StringVar(&containerID, "container", "", "GTM Container ID")
	requireFlag(liveCmd, "account", "container")

	getCmd := &cobra.Command{
		Use:   "get",
		Short: "Get a specific container version",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			v, err := client.GetVersion(cmd.Context(), accountID, containerID, versionID)
			if err != nil {
				return err
			}
			return p.PrintTable(v, []string{"VERSION_ID", "NAME", "TAGS", "TRIGGERS", "VARIABLES", "FINGERPRINT"}, [][]string{
				{v.ContainerVersionId, orDash(v.Name), fmt.Sprintf("%d", len(v.Tag)), fmt.Sprintf("%d", len(v.Trigger)), fmt.Sprintf("%d", len(v.Variable)), orDash(v.Fingerprint)},
			})
		},
	}
	getCmd.Flags().StringVar(&accountID, "account", "", "GTM Account ID")
	getCmd.Flags().StringVar(&containerID, "container", "", "GTM Container ID")
	getCmd.Flags().StringVar(&versionID, "version", "", "GTM Container Version ID")
	requireFlag(getCmd, "account", "container", "version")

	publishCmd := &cobra.Command{
		Use:   "publish",
		Short: "Publish a container version to live (requires --mode full --confirm)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, client, err := prepPrinterAndClient(cmd, app)
			if err != nil {
				return err
			}
			res, err := client.PublishVersion(cmd.Context(), accountID, containerID, versionID, fingerprint, app.opts.Confirm)
			if err != nil {
				return err
			}
			verID, verName := versionID, "-"
			if res.ContainerVersion != nil {
				verID = res.ContainerVersion.ContainerVersionId
				verName = res.ContainerVersion.Name
			}
			return p.PrintTable(res, []string{"PUBLISHED_VERSION_ID", "NAME", "COMPILER_ERROR"}, [][]string{
				{verID, orDash(verName), boolStr(res.CompilerError)},
			})
		},
	}
	publishCmd.Flags().StringVar(&accountID, "account", "", "GTM Account ID")
	publishCmd.Flags().StringVar(&containerID, "container", "", "GTM Container ID")
	publishCmd.Flags().StringVar(&versionID, "version", "", "GTM Container Version ID")
	publishCmd.Flags().StringVar(&fingerprint, "fingerprint", "", "Version fingerprint from create-version or get")
	requireFlag(publishCmd, "account", "container", "version", "fingerprint")

	cmd.AddCommand(listCmd, liveCmd, getCmd, publishCmd)
	return cmd
}
