package cli

import "github.com/spf13/cobra"

// Root returns the root command for the cloud CLI.
func Root() *cobra.Command {
	root := &cobra.Command{
		Use:          "nvoi",
		Short:        "Deploy containers to cloud servers",
		SilenceUsage: true,
	}

	root.AddCommand(newLoginCmd())
	root.AddCommand(newWhoamiCmd())
	root.AddCommand(newWorkspacesCmd())
	root.AddCommand(newReposCmd())
	root.AddCommand(newPushCmd())
	root.AddCommand(newPlanCmd())
	root.AddCommand(newDeployCmd())
	root.AddCommand(newLogsCmd())
	root.AddCommand(newSSHCmd())
	root.AddCommand(newDescribeCmd())
	root.AddCommand(newResourcesCmd())
	root.AddCommand(newDatabaseCmd())
	root.AddCommand(newAgentCmd())

	return root
}
