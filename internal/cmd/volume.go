package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newVolumeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "volume",
		Short: "Manage block storage volumes",
	}
	cmd.AddCommand(newVolumeSetCmd())
	cmd.AddCommand(newVolumeDeleteCmd())
	cmd.AddCommand(newVolumeListCmd())
	return cmd
}

func newVolumeSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set [name]",
		Short: "Provision or reconcile a block storage volume",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			size, _ := cmd.Flags().GetInt("size")
			server, _ := cmd.Flags().GetString("server")

			_ = name
			_ = size
			_ = server

			// TODO Phase 3:
			// 1. Resolve compute provider
			// 2. Get target server by name from provider API: nvoi-{env}-{server}
			// 3. EnsureVolume by name (idempotent — provider API)
			// 4. AttachVolume to server (provider API)
			// 5. Resolve device path (provider API + SSH ls /dev/disk/by-id/ fallback)
			// 6. Wait for device node (SSH: test -b)
			// 7. Format XFS if needed (SSH: blkid + mkfs.xfs)
			// 8. Mount + fstab entry (SSH: mount + tee -a /etc/fstab)
			// 9. Write VOLUME_{NAME}_PATH to .env
			return fmt.Errorf("not implemented")
		},
	}
	cmd.Flags().Int("size", 10, "volume size in GB")
	cmd.Flags().String("server", "master", "target server name")
	return cmd
}

func newVolumeDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete [name]",
		Short: "Detach a volume (data preserved)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO Phase 4:
			// 1. Resolve provider
			// 2. Find volume by name: nvoi-{env}-{name} (provider API)
			// 3. DetachVolume (provider API — data preserved, volume NOT deleted)
			// 4. Remove VOLUME_* from .env
			return fmt.Errorf("not implemented")
		},
	}
}

func newVolumeListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List volumes",
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO Phase 3:
			// 1. Resolve provider
			// 2. Query provider API: list volumes by label nvoi/stack={env}
			// 3. Print table (name, size, attached server, mount path)
			return fmt.Errorf("not implemented")
		},
	}
}
