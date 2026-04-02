package cmd

import (
	"fmt"

	"github.com/getnvoi/nvoi/pkg/app"
	_ "github.com/getnvoi/nvoi/pkg/provider/cloudflare" // register cloudflare R2
	"github.com/spf13/cobra"
)

func newStorageCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "storage",
		Short: "Manage object storage buckets",
	}
	cmd.AddCommand(newStorageSetCmd())
	cmd.AddCommand(newStorageEmptyCmd())
	cmd.AddCommand(newStorageDeleteCmd())
	cmd.AddCommand(newStorageListCmd())
	return cmd
}

func newStorageSetCmd() *cobra.Command {
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
			name := args[0]
			bucketName, _ := cmd.Flags().GetString("bucket")
			cors, _ := cmd.Flags().GetBool("cors")
			expireDays, _ := cmd.Flags().GetInt("expire-days")

			appName, env, err := resolveAppEnv(cmd)
			if err != nil {
				return err
			}
			storageProvider, err := resolveStorageProvider(cmd)
			if err != nil {
				return err
			}
			storageCreds, err := resolveStorageCredentials(storageProvider)
			if err != nil {
				return err
			}
			computeProvider, err := resolveComputeProvider(cmd)
			if err != nil {
				return err
			}
			computeCreds, err := resolveComputeCredentials(cmd, computeProvider)
			if err != nil {
				return err
			}
			sshKey, err := resolveSSHKey()
			if err != nil {
				return err
			}

			return app.StorageSet(cmd.Context(), app.StorageSetRequest{
				Cluster: app.Cluster{
					AppName:     appName,
					Env:         env,
					Provider:    computeProvider,
					Credentials: computeCreds,
					SSHKey:      sshKey,
					Output:      resolveOutput(cmd),
				},
				StorageProvider: storageProvider,
				StorageCreds:    storageCreds,
				Name:            name,
				Bucket:          bucketName,
				CORS:            cors,
				ExpireDays:      expireDays,
			})
		},
	}
	addComputeProviderFlags(cmd)
	addStorageProviderFlags(cmd)
	addAppFlags(cmd)
	cmd.Flags().String("bucket", "", "explicit bucket name (default: nvoi-{app}-{env}-{name})")
	cmd.Flags().Bool("cors", false, "enable CORS (allows all origins)")
	cmd.Flags().Int("expire-days", 0, "object expiration in days (0 = disabled)")
	return cmd
}

func newStorageEmptyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "empty [name]",
		Short: "Delete all objects in a storage bucket",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			yes, _ := cmd.Flags().GetBool("yes")

			if !yes {
				fmt.Printf("Empty all objects in storage %s? This cannot be undone. [y/N] ", args[0])
				var confirm string
				fmt.Scanln(&confirm)
				if confirm != "y" && confirm != "yes" {
					fmt.Println("aborted.")
					return nil
				}
			}

			appName, env, err := resolveAppEnv(cmd)
			if err != nil {
				return err
			}
			storageProvider, err := resolveStorageProvider(cmd)
			if err != nil {
				return err
			}
			storageCreds, err := resolveStorageCredentials(storageProvider)
			if err != nil {
				return err
			}

			return app.StorageEmpty(cmd.Context(), app.StorageEmptyRequest{
				Cluster: app.Cluster{
					AppName: appName,
					Env:     env,
					Output:  resolveOutput(cmd),
				},
				StorageProvider: storageProvider,
				StorageCreds:    storageCreds,
				Name:            args[0],
			})
		},
	}
	addStorageProviderFlags(cmd)
	addAppFlags(cmd)
	cmd.Flags().BoolP("yes", "y", false, "skip confirmation")
	return cmd
}

func newStorageDeleteCmd() *cobra.Command {
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
			yes, _ := cmd.Flags().GetBool("yes")

			if !yes {
				fmt.Printf("Delete storage %s (bucket + secrets)? [y/N] ", args[0])
				var confirm string
				fmt.Scanln(&confirm)
				if confirm != "y" && confirm != "yes" {
					fmt.Println("aborted.")
					return nil
				}
			}

			appName, env, err := resolveAppEnv(cmd)
			if err != nil {
				return err
			}
			storageProvider, err := resolveStorageProvider(cmd)
			if err != nil {
				return err
			}
			storageCreds, err := resolveStorageCredentials(storageProvider)
			if err != nil {
				return err
			}
			computeProvider, err := resolveComputeProvider(cmd)
			if err != nil {
				return err
			}
			computeCreds, err := resolveComputeCredentials(cmd, computeProvider)
			if err != nil {
				return err
			}
			sshKey, err := resolveSSHKey()
			if err != nil {
				return err
			}

			return app.StorageDelete(cmd.Context(), app.StorageDeleteRequest{
				Cluster: app.Cluster{
					AppName:     appName,
					Env:         env,
					Provider:    computeProvider,
					Credentials: computeCreds,
					SSHKey:      sshKey,
					Output:      resolveOutput(cmd),
				},
				StorageProvider: storageProvider,
				StorageCreds:    storageCreds,
				Name:            args[0],
			})
		},
	}
	addComputeProviderFlags(cmd)
	addStorageProviderFlags(cmd)
	addAppFlags(cmd)
	cmd.Flags().BoolP("yes", "y", false, "skip confirmation")
	return cmd
}

func newStorageListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List storage buckets configured in the cluster",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			appName, env, err := resolveAppEnv(cmd)
			if err != nil {
				return err
			}
			computeProvider, err := resolveComputeProvider(cmd)
			if err != nil {
				return err
			}
			computeCreds, err := resolveComputeCredentials(cmd, computeProvider)
			if err != nil {
				return err
			}
			sshKey, err := resolveSSHKey()
			if err != nil {
				return err
			}

			items, err := app.StorageList(cmd.Context(), app.StorageListRequest{
				Cluster: app.Cluster{
					AppName:     appName,
					Env:         env,
					Provider:    computeProvider,
					Credentials: computeCreds,
					SSHKey:      sshKey,
					Output:      resolveOutput(cmd),
				},
			})
			if err != nil {
				return err
			}

			if len(items) == 0 {
				fmt.Println("no storage configured")
				return nil
			}

			for _, item := range items {
				fmt.Printf("%-20s %s\n", item.Name, item.Bucket)
			}
			return nil
		},
	}
	addComputeProviderFlags(cmd)
	addAppFlags(cmd)
	return cmd
}
