package commands

import "github.com/spf13/cobra"

// NewVolumeCmd returns the volume command group.
func NewVolumeCmd(b Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "volume",
		Short: "Manage block storage volumes",
	}
	cmd.AddCommand(newVolumeSetCmd(b))
	cmd.AddCommand(newVolumeDeleteCmd(b))
	cmd.AddCommand(newVolumeListCmd(b))
	return cmd
}

func newVolumeSetCmd(b Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set [name]",
		Short: "Provision or reconcile a block storage volume",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			size, _ := cmd.Flags().GetInt("size")
			server, _ := cmd.Flags().GetString("server")
			return b.VolumeSet(cmd.Context(), args[0], size, server)
		},
	}
	cmd.Flags().Int("size", 10, "volume size in GB")
	cmd.Flags().String("server", "master", "target server name")
	_ = cmd.MarkFlagRequired("size")
	return cmd
}

func newVolumeDeleteCmd(b Backend) *cobra.Command {
	return &cobra.Command{
		Use:   "delete [name]",
		Short: "Detach a volume (data preserved)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return b.VolumeDelete(cmd.Context(), args[0])
		},
	}
}

func newVolumeListCmd(b Backend) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List volumes",
		RunE: func(cmd *cobra.Command, args []string) error {
			return b.VolumeList(cmd.Context())
		},
	}
}
