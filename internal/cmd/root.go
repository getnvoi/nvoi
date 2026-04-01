package cmd

import "github.com/spf13/cobra"

func Root() *cobra.Command {
	root := &cobra.Command{
		Use:           "nvoi",
		Short:         "Deploy containers to cloud servers",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Persistent flags.
	root.PersistentFlags().String("env-file", ".env", "path to .env file")

	// Infrastructure.
	root.AddCommand(newInstanceCmd())
	root.AddCommand(newVolumeCmd())
	root.AddCommand(newDNSCmd())
	root.AddCommand(newStorageCmd())

	// Application.
	root.AddCommand(newServiceCmd())
	root.AddCommand(newSecretCmd())

	// Build.
	root.AddCommand(newBuildCmd())

	// Live view.
	root.AddCommand(newShowCmd())

	// Operate.
	root.AddCommand(newLogsCmd())
	root.AddCommand(newExecCmd())
	root.AddCommand(newSSHCmd())

	// Inspect.
	root.AddCommand(newResourcesCmd())

	// Teardown — use bin/destroy (the script). No CLI command needed.

	return root
}

func envFilePath(cmd *cobra.Command) string {
	p, _ := cmd.Flags().GetString("env-file")
	return p
}
