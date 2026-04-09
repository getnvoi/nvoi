package core

import (
	"fmt"
	"os"
	"strings"

	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/infra"
	"github.com/getnvoi/nvoi/pkg/managed"
	"github.com/getnvoi/nvoi/pkg/utils"
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
  nvoi database set db --type postgres --secret POSTGRES_PASSWORD --secret POSTGRES_USER --secret POSTGRES_DB
  nvoi database set db --type postgres --image postgres:16 --secret POSTGRES_PASSWORD --secret POSTGRES_USER --secret POSTGRES_DB --backup-storage db-backups --backup-cron "0 2 * * *"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind, err := resolveDatabaseType(cmd)
			if err != nil {
				return err
			}
			image, _ := cmd.Flags().GetString("image")
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

			// Build backup image if backup is configured.
			var backupImage string
			if backupStorage != "" && backupCron != "" {
				var err error
				backupImage, err = ensureBackupImage(cmd, cluster, image)
				if err != nil {
					return err
				}
			}

			result, err := managed.Compile(managed.Request{
				Kind:          kind,
				Name:          args[0],
				Image:         image,
				Env:           env,
				BackupStorage: backupStorage,
				BackupCron:    backupCron,
				BackupImage:   backupImage,
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
	cmd.Flags().String("image", "", "database image (default: postgres:17)")
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

			storageName, _ := cmd.Flags().GetString("backup-storage")
			if storageName == "" {
				storageName = utils.BackupStorageName(args[0])
			}
			if err := verifyStorageExists(cmd, cluster, storageName); err != nil {
				return err
			}
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
	cmd.Flags().String("backup-storage", "", "storage name for backups (default: {name}-backups)")
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

			storageName, _ := cmd.Flags().GetString("backup-storage")
			if storageName == "" {
				storageName = utils.BackupStorageName(args[0])
			}
			if err := verifyStorageExists(cmd, cluster, storageName); err != nil {
				return err
			}
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
	cmd.Flags().String("backup-storage", "", "storage name for backups (default: {name}-backups)")
	return cmd
}

// ensureBackupImage builds and pushes the postgres backup image if it doesn't
// exist in the cluster registry. Returns the full image ref.
// The image is postgres + aws cli. One per postgres version, shared across databases.
func ensureBackupImage(cmd *cobra.Command, cluster app.Cluster, baseImage string) (string, error) {
	if baseImage == "" {
		baseImage = "postgres:17"
	}

	version := "latest"
	if parts := strings.SplitN(baseImage, ":", 2); len(parts) == 2 {
		version = parts[1]
	}

	imageName := "nvoi-pg-backup"
	cluster.Output.Progress(fmt.Sprintf("ensuring backup image %s:%s", imageName, version))

	master, _, _, err := cluster.Master(cmd.Context())
	if err != nil {
		return "", err
	}
	registryAddr := master.PrivateIP + ":5000"
	fullRef := registryAddr + "/" + imageName + ":" + version

	ssh, err := infra.ConnectSSH(cmd.Context(), master.IPv4+":22", utils.DefaultUser, cluster.SSHKey)
	if err != nil {
		return "", err
	}
	defer ssh.Close()

	// Check if image already exists in registry.
	checkCmd := fmt.Sprintf("curl -sf http://%s/v2/%s/tags/list 2>/dev/null | grep -q %q", registryAddr, imageName, version)
	if _, err := ssh.Run(cmd.Context(), checkCmd); err == nil {
		cluster.Output.Success(fmt.Sprintf("backup image %s exists", fullRef))
		return fullRef, nil
	}

	// Upload Dockerfile then buildx with insecure push (same as all other builders).
	dockerfile := fmt.Sprintf("FROM %s\nRUN apt-get update && apt-get install -y awscli && rm -rf /var/lib/apt/lists/*\n", baseImage)
	dfPath := "/tmp/nvoi-pg-backup.Dockerfile"
	if err := ssh.Upload(cmd.Context(), strings.NewReader(dockerfile), dfPath, 0644); err != nil {
		return "", fmt.Errorf("upload Dockerfile: %w", err)
	}

	cluster.Output.Progress(fmt.Sprintf("building backup image %s", fullRef))
	buildCmd := fmt.Sprintf("docker buildx build --tag %s --output type=image,push=true,registry.insecure=true -f %s /tmp", fullRef, dfPath)
	if _, err := ssh.Run(cmd.Context(), buildCmd); err != nil {
		return "", fmt.Errorf("build backup image: %w", err)
	}

	cluster.Output.Success(fmt.Sprintf("backup image %s ready", fullRef))
	return fullRef, nil
}
