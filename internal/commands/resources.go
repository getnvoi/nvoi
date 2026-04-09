package commands

import "github.com/spf13/cobra"

// NewResourcesCmd returns the resources command.
func NewResourcesCmd(b Backend) *cobra.Command {
	return &cobra.Command{
		Use:   "resources",
		Short: "List all resources under the provider accounts",
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonOutput, _ := cmd.Flags().GetBool("json")
			return b.Resources(cmd.Context(), jsonOutput)
		},
	}
}
