package cli

import (
	"github.com/spf13/cobra"
)

func Root() *cobra.Command {
	root := &cobra.Command{
		Use:          "nvoi",
		Short:        "Deploy containers to cloud servers",
		SilenceUsage: true,
	}

	// All commands need auth — PersistentPreRunE loads client.
	// Standalone commands (login, whoami, etc.) override it.
	var client *APIClient
	var wsID, repoID string

	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		c, cfg, err := authedClient()
		if err != nil {
			return err
		}
		ws, repo, err := requireRepo(cfg)
		if err != nil {
			return err
		}
		client = c
		wsID = ws
		repoID = repo
		return nil
	}

	repoPath := func(suffix string) string {
		return "/workspaces/" + wsID + "/repos/" + repoID + suffix
	}

	root.AddCommand(newCloudDeployCmd(&client, &repoPath))
	root.AddCommand(newCloudDestroyCmd(&client, &repoPath))
	root.AddCommand(newCloudDescribeCmd(&client, &repoPath))
	root.AddCommand(newCloudResourcesCmd(&client, &repoPath))
	root.AddCommand(newCloudLogsCmd(&client, &repoPath))
	root.AddCommand(newCloudExecCmd(&client, &repoPath))
	root.AddCommand(newCloudSSHCmd(&client, &repoPath))

	addStandalone(root, newLoginCmd())
	addStandalone(root, newWhoamiCmd())
	addStandalone(root, newWorkspacesCmd())
	addStandalone(root, newReposCmd())
	addStandalone(root, newProviderCmd())

	return root
}

func addStandalone(root *cobra.Command, cmd *cobra.Command) {
	cmd.PersistentPreRunE = func(*cobra.Command, []string) error { return nil }
	root.AddCommand(cmd)
}
