package commands

import "github.com/spf13/cobra"

// NewDescribeCmd returns the describe command.
func NewDescribeCmd(b Backend) *cobra.Command {
	return &cobra.Command{
		Use:   "describe",
		Short: "Describe the cluster — nodes, workloads, pods, services, ingress, secrets",
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonOutput, _ := cmd.Flags().GetBool("json")
			return b.Describe(cmd.Context(), jsonOutput)
		},
	}
}
