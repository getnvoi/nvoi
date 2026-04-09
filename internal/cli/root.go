package cli

import (
	"github.com/getnvoi/nvoi/internal/commands"
	"github.com/spf13/cobra"
)

// Root returns the root command for the cloud CLI.
func Root() *cobra.Command {
	d := &CloudBackend{}

	root := &cobra.Command{
		Use:          "nvoi",
		Short:        "Deploy containers to cloud servers",
		SilenceUsage: true,
		// Builds the CloudBackend before any shared command runs.
		// Standalone cloud commands (login, whoami, etc.) override this with a no-op.
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			b, err := buildCloudBackend()
			if err != nil {
				return err
			}
			*d = *b
			return nil
		},
	}

	// Shared commands — inherit PersistentPreRunE, get backend.
	root.AddCommand(commands.NewInstanceCmd(d))
	root.AddCommand(commands.NewFirewallCmd(d))
	root.AddCommand(commands.NewVolumeCmd(d))
	root.AddCommand(commands.NewDNSCmd(d))
	root.AddCommand(commands.NewIngressCmd(d))
	root.AddCommand(commands.NewStorageCmd(d))
	root.AddCommand(commands.NewServiceCmd(d))
	root.AddCommand(commands.NewCronCmd(d))
	root.AddCommand(commands.NewSecretCmd(d))
	root.AddCommand(commands.NewDatabaseCmd(d))
	root.AddCommand(commands.NewAgentCmd(d))
	root.AddCommand(commands.NewBuildCmd(d))
	root.AddCommand(commands.NewDescribeCmd(d))
	root.AddCommand(commands.NewLogsCmd(d))
	root.AddCommand(commands.NewExecCmd(d))
	root.AddCommand(commands.NewSSHCmd(d))
	root.AddCommand(commands.NewResourcesCmd(d))

	// Cloud-only standalone commands — override PersistentPreRunE (no backend needed).
	addStandalone(root, newLoginCmd())
	addStandalone(root, newWhoamiCmd())
	addStandalone(root, newWorkspacesCmd())
	addStandalone(root, newReposCmd())
	addStandalone(root, newProviderCmd())
	addStandalone(root, newPushCmd())
	addStandalone(root, newPlanCmd())
	addStandalone(root, newDeployCmd())
	addStandalone(root, newDeployLogsCmd())

	return root
}

// addStandalone adds a command that doesn't need the CloudBackend.
// Overrides root's PersistentPreRunE so auth isn't required.
func addStandalone(root *cobra.Command, cmd *cobra.Command) {
	cmd.PersistentPreRunE = func(*cobra.Command, []string) error { return nil }
	root.AddCommand(cmd)
}
