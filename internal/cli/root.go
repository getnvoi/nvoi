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

	return root
}
