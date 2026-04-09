package core

import (
	"fmt"
	"os"
	"strings"

	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/managed"
	"github.com/spf13/cobra"
)

func newDatabaseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "database",
		Short: "Manage databases",
	}
	cmd.AddCommand(newDatabaseSetCmd())
	cmd.AddCommand(newDatabaseDeleteCmd())
	cmd.AddCommand(newDatabaseListCmd())
	cmd.AddCommand(newDatabaseBackupCmd())
	return cmd
}

func resolveDatabaseType(cmd *cobra.Command) (string, error) {
	kind, _ := cmd.Flags().GetString("type")
	if kind == "" {
		available := managed.KindsForCategory("database")
		return "", fmt.Errorf("--type is required. Available database types: %s", strings.Join(available, ", "))
	}
	return kind, nil
}

func newDatabaseSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set [name]",
		Short: "Deploy a managed database to the cluster",
		Long: `Compiles a database bundle and executes all owned primitive operations.

Required credentials are read from the cluster via --secret.
Backup storage must be pre-created via 'nvoi storage set'.

Examples:
  nvoi database set db --type postgres --secret POSTGRES_PASSWORD --backup-storage db-backups --backup-cron "0 2 * * *"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind, err := resolveDatabaseType(cmd)
			if err != nil {
				return err
			}
			secrets, _ := cmd.Flags().GetStringArray("secret")
			backupStorage, _ := cmd.Flags().GetString("backup-storage")
			backupCron, _ := cmd.Flags().GetString("backup-cron")

			cluster, err := resolveCluster(cmd)
			if err != nil {
				return err
			}

			env, err := readSecretsFromCluster(cmd, cluster, secrets)
			if err != nil {
				return err
			}

			// Verify backup storage exists if specified.
			if backupStorage != "" {
				if err := verifyStorageExists(cmd, cluster, backupStorage); err != nil {
					return err
				}
			}

			// Upload s3upload binary to server if backup is configured.
			if backupStorage != "" && backupCron != "" {
				if err := uploadS3UploadBinary(cmd, cluster); err != nil {
					return err
				}
			}

			result, err := managed.Compile(managed.Request{
				Kind:          kind,
				Name:          args[0],
				Env:           env,
				BackupStorage: backupStorage,
				BackupCron:    backupCron,
				Context:       managed.Context{DefaultVolumeServer: "master"},
			})
			if err != nil {
				return err
			}

			for _, op := range result.Bundle.Operations {
				if err := execOperation(cmd.Context(), cluster, op); err != nil {
					return err
				}
			}
			return nil
		},
	}
	addComputeProviderFlags(cmd)
	addAppFlags(cmd)
	cmd.Flags().String("type", "", "database type (postgres)")
	cmd.Flags().StringArray("secret", nil, "secret key to read from cluster")
	cmd.Flags().String("backup-storage", "", "pre-existing storage name for backups")
	cmd.Flags().String("backup-cron", "", "cron schedule for backups (e.g. \"0 2 * * *\")")
	return cmd
}

func newDatabaseDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete [name]",
		Short: "Delete a managed database and all owned resources",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind, err := resolveDatabaseType(cmd)
			if err != nil {
				return err
			}
			yes, _ := cmd.Flags().GetBool("yes")
			if !yes {
				fmt.Printf("Delete database %s and all owned resources? [y/N] ", args[0])
				var confirm string
				fmt.Scanln(&confirm)
				if confirm != "y" && confirm != "yes" {
					fmt.Println("aborted.")
					return nil
				}
			}

			cluster, err := resolveCluster(cmd)
			if err != nil {
				return err
			}

			shape, err := managed.Shape(kind, args[0])
			if err != nil {
				return err
			}

			return deleteByShape(cmd, cluster, shape)
		},
	}
	addComputeProviderFlags(cmd)
	addAppFlags(cmd)
	cmd.Flags().String("type", "", "database type (postgres)")
	cmd.Flags().BoolP("yes", "y", false, "skip confirmation")
	return cmd
}

func newDatabaseListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List managed databases",
		RunE: func(cmd *cobra.Command, args []string) error {
			cluster, err := resolveCluster(cmd)
			if err != nil {
				return err
			}

			// List all database-category kinds.
			kinds := managed.KindsForCategory("database")
			var all []app.ManagedService
			for _, kind := range kinds {
				services, err := app.ManagedList(cmd.Context(), app.ManagedListRequest{
					Cluster: cluster,
					Kind:    kind,
				})
				if err != nil {
					return err
				}
				all = append(all, services...)
			}

			if len(all) == 0 {
				cluster.Output.Info("no managed databases found")
				return nil
			}
			for _, svc := range all {
				children := strings.Join(svc.Children, ", ")
				cluster.Output.Success(fmt.Sprintf("%s  type=%s  %s  %s  children=[%s]", svc.Name, svc.ManagedKind, svc.Image, svc.Ready, children))
			}
			return nil
		},
	}
	addComputeProviderFlags(cmd)
	addAppFlags(cmd)
	return cmd
}

// ── Backup subcommand ─────────────────────────────────────────────────────────

func newDatabaseBackupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Manage database backups",
	}
	cmd.AddCommand(newDatabaseBackupCreateCmd())
	cmd.AddCommand(newDatabaseBackupListCmd())
	cmd.AddCommand(newDatabaseBackupDownloadCmd())
	return cmd
}

func newDatabaseBackupCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create [name]",
		Short: "Create a backup now",
		Long: `Triggers the backup cron job immediately.

Examples:
  nvoi database backup create db --type postgres`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind, err := resolveDatabaseType(cmd)
			if err != nil {
				return err
			}

			cluster, err := resolveCluster(cmd)
			if err != nil {
				return err
			}

			if err := verifyManagedKind(cmd, cluster, args[0], kind); err != nil {
				return err
			}

			cronName := args[0] + "-backup"
			return app.BackupCreate(cmd.Context(), app.BackupCreateRequest{
				Cluster:  cluster,
				CronName: cronName,
			})
		},
	}
	addComputeProviderFlags(cmd)
	addAppFlags(cmd)
	cmd.Flags().String("type", "", "database type (postgres)")
	return cmd
}

func newDatabaseBackupListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list [name]",
		Short: "List backup artifacts",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind, err := resolveDatabaseType(cmd)
			if err != nil {
				return err
			}

			cluster, err := resolveCluster(cmd)
			if err != nil {
				return err
			}

			if err := verifyManagedKind(cmd, cluster, args[0], kind); err != nil {
				return err
			}

			storageName := args[0] + "-backups"
			artifacts, err := app.BackupList(cmd.Context(), app.BackupListRequest{
				Cluster: cluster,
				Name:    storageName,
			})
			if err != nil {
				return err
			}

			if len(artifacts) == 0 {
				cluster.Output.Info("no backups found")
				return nil
			}
			for _, a := range artifacts {
				cluster.Output.Success(fmt.Sprintf("%s  %d bytes  %s", a.Key, a.Size, a.LastModified))
			}
			return nil
		},
	}
	addComputeProviderFlags(cmd)
	addAppFlags(cmd)
	cmd.Flags().String("type", "", "database type (postgres)")
	return cmd
}

func newDatabaseBackupDownloadCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "download [name] [artifact]",
		Short: "Download a backup artifact",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind, err := resolveDatabaseType(cmd)
			if err != nil {
				return err
			}

			cluster, err := resolveCluster(cmd)
			if err != nil {
				return err
			}

			if err := verifyManagedKind(cmd, cluster, args[0], kind); err != nil {
				return err
			}

			storageName := args[0] + "-backups"
			data, err := app.BackupDownload(cmd.Context(), app.BackupDownloadRequest{
				Cluster: cluster,
				Name:    storageName,
				Key:     args[1],
			})
			if err != nil {
				return err
			}

			_, err = os.Stdout.Write(data)
			return err
		},
	}
	addComputeProviderFlags(cmd)
	addAppFlags(cmd)
	cmd.Flags().String("type", "", "database type (postgres)")
	return cmd
}
