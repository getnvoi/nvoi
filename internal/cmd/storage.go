package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newStorageCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "storage",
		Short: "Manage object storage buckets",
	}
	cmd.AddCommand(newStorageSetCmd())
	cmd.AddCommand(newStorageDeleteCmd())
	return cmd
}

func newStorageSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set [name]",
		Short: "Provision or reconcile an object storage bucket",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			providerName, _ := cmd.Flags().GetString("provider")
			bucketName, _ := cmd.Flags().GetString("bucket")
			cors, _ := cmd.Flags().GetBool("cors")
			expireDays, _ := cmd.Flags().GetInt("expire-days")

			_ = name
			_ = providerName
			_ = bucketName
			_ = cors
			_ = expireDays

			// TODO Phase 4:
			// 1. Resolve bucket provider from --provider flag
			// 2. EnsureBucket (idempotent — bucket API)
			// 3. SetCORS if --cors (bucket API)
			// 4. SetLifecycle if --expire-days > 0 (bucket API)
			// 5. Get credentials (bucket API)
			// 6. Write STORAGE_{NAME}_ENDPOINT, _ACCESS_KEY, _SECRET_KEY to .env
			return fmt.Errorf("not implemented")
		},
	}
	cmd.Flags().String("provider", "", "storage provider (cloudflare, aws, hetzner)")
	cmd.Flags().String("bucket", "", "bucket name")
	cmd.Flags().Bool("cors", false, "enable CORS")
	cmd.Flags().Int("expire-days", 0, "object expiration in days (0 = disabled)")
	_ = cmd.MarkFlagRequired("provider")
	_ = cmd.MarkFlagRequired("bucket")
	return cmd
}

func newStorageDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete [name]",
		Short: "Remove storage credentials (bucket preserved in cloud)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO Phase 4:
			// Bucket is NOT deleted in cloud — data preservation.
			// 1. Remove STORAGE_* from .env
			return fmt.Errorf("not implemented")
		},
	}
}
