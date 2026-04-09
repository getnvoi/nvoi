package commands

import "github.com/spf13/cobra"

// NewStorageCmd returns the storage command group.
func NewStorageCmd(b Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "storage",
		Short: "Manage object storage buckets",
	}
	cmd.AddCommand(newStorageSetCmd(b))
	cmd.AddCommand(newStorageEmptyCmd(b))
	cmd.AddCommand(newStorageDeleteCmd(b))
	cmd.AddCommand(newStorageListCmd(b))
	return cmd
}

func newStorageSetCmd(b Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set [name]",
		Short: "Provision or reconcile an object storage bucket",
		Long: `Creates or reconciles an R2 bucket and stores S3-compatible credentials
as k8s secrets with conventional names (STORAGE_{NAME}_*).

Bucket name defaults to nvoi-{app}-{env}-{name}. Override with --bucket.

Examples:
  nvoi storage set assets                          # bucket: nvoi-rails-production-assets
  nvoi storage set uploads --cors                  # with CORS enabled
  nvoi storage set tmp --expire-days 30            # auto-expire objects
  nvoi storage set assets --bucket custom-name     # explicit bucket name

Then reference on service set:
  nvoi service set web --image $IMAGE --storage assets --storage uploads`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			bucket, _ := cmd.Flags().GetString("bucket")
			cors, _ := cmd.Flags().GetBool("cors")
			expireDays, _ := cmd.Flags().GetInt("expire-days")
			return b.StorageSet(cmd.Context(), args[0], bucket, cors, expireDays)
		},
	}
	cmd.Flags().String("bucket", "", "explicit bucket name (default: nvoi-{app}-{env}-{name})")
	cmd.Flags().Bool("cors", false, "enable CORS (allows all origins)")
	cmd.Flags().Int("expire-days", 0, "object expiration in days (0 = disabled)")
	return cmd
}

func newStorageEmptyCmd(b Backend) *cobra.Command {
	return &cobra.Command{
		Use:   "empty [name]",
		Short: "Delete all objects in a storage bucket",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return b.StorageEmpty(cmd.Context(), args[0])
		},
	}
}

func newStorageDeleteCmd(b Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete [name]",
		Short: "Delete storage bucket and remove secrets from cluster",
		Long: `Deletes the bucket from the provider and removes STORAGE_{NAME}_* secrets.
Bucket must be empty first — use 'storage empty' or the provider will reject.

Examples:
  nvoi storage empty assets -y
  nvoi storage delete assets -y`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return b.StorageDelete(cmd.Context(), args[0])
		},
	}
	return cmd
}

func newStorageListCmd(b Backend) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List storage buckets configured in the cluster",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return b.StorageList(cmd.Context())
		},
	}
}
