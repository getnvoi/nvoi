package commands

import "github.com/spf13/cobra"

// NewInstanceCmd returns the instance command group.
func NewInstanceCmd(b Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "instance",
		Short: "Manage compute instances",
	}
	cmd.AddCommand(newInstanceSetCmd(b))
	cmd.AddCommand(newInstanceDeleteCmd(b))
	cmd.AddCommand(newInstanceListCmd(b))
	return cmd
}

func newInstanceSetCmd(b Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set [name]",
		Short: "Provision an instance and install k3s",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			computeType, _ := cmd.Flags().GetString("compute-type")
			region, _ := cmd.Flags().GetString("compute-region")
			role, _ := cmd.Flags().GetString("role")
			return b.InstanceSet(cmd.Context(), args[0], computeType, region, role)
		},
	}
	cmd.Flags().String("compute-type", "", "instance type (e.g. cax11)")
	cmd.Flags().String("compute-region", "", "instance region (e.g. fsn1)")
	cmd.Flags().String("role", "", "server role: master or worker (required)")
	_ = cmd.MarkFlagRequired("compute-type")
	_ = cmd.MarkFlagRequired("compute-region")
	_ = cmd.MarkFlagRequired("role")
	return cmd
}

func newInstanceDeleteCmd(b Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete [name]",
		Short: "Delete an instance (firewall + network cleaned up)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return b.InstanceDelete(cmd.Context(), args[0])
		},
	}
	return cmd
}

func newInstanceListCmd(b Backend) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List provisioned instances",
		RunE: func(cmd *cobra.Command, args []string) error {
			return b.InstanceList(cmd.Context())
		},
	}
}
