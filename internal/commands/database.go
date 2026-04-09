package commands

import "github.com/spf13/cobra"

// NewDatabaseCmd returns the database command group.
func NewDatabaseCmd(b Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "database",
		Short: "Manage databases",
	}
	cmd.AddCommand(newDatabaseSetCmd(b))
	cmd.AddCommand(newDatabaseDeleteCmd(b))
	cmd.AddCommand(newDatabaseListCmd(b))
	cmd.AddCommand(newDatabaseBackupCmd(b))
	return cmd
}

func newDatabaseSetCmd(b Backend) *cobra.Command {
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
			kind, err := resolveManagedKind(cmd, "database")
			if err != nil {
				return err
			}
			secrets, _ := cmd.Flags().GetStringArray("secret")
			image, _ := cmd.Flags().GetString("image")
			volumeSize, _ := cmd.Flags().GetInt("volume-size")
			backupStorage, _ := cmd.Flags().GetString("backup-storage")
			backupCron, _ := cmd.Flags().GetString("backup-cron")

			return b.DatabaseSet(cmd.Context(), args[0], ManagedOpts{
				Kind:          kind,
				Secrets:       secrets,
				Image:         image,
				VolumeSize:    volumeSize,
				BackupStorage: backupStorage,
				BackupCron:    backupCron,
			})
		},
	}
	cmd.Flags().String("type", "", "database type (postgres)")
	cmd.Flags().String("image", "", "database image (default: postgres:17)")
	cmd.Flags().Int("volume-size", 0, "data volume size in GB (default: 10)")
	cmd.Flags().StringArray("secret", nil, "secret key to read from cluster")
	cmd.Flags().String("backup-storage", "", "pre-existing storage name for backups")
	cmd.Flags().String("backup-cron", "", "cron schedule for backups (e.g. \"0 2 * * *\")")
	return cmd
}

func newDatabaseDeleteCmd(b Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete [name]",
		Short: "Delete a managed database and all owned resources",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind, err := resolveManagedKind(cmd, "database")
			if err != nil {
				return err
			}
			return b.DatabaseDelete(cmd.Context(), args[0], kind)
		},
	}
	cmd.Flags().String("type", "", "database type (postgres)")
	return cmd
}

func newDatabaseListCmd(b Backend) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List managed databases",
		RunE: func(cmd *cobra.Command, args []string) error {
			return b.DatabaseList(cmd.Context())
		},
	}
}

func newDatabaseBackupCmd(b Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Manage database backups",
	}
	cmd.AddCommand(newDatabaseBackupCreateCmd(b))
	cmd.AddCommand(newDatabaseBackupListCmd(b))
	cmd.AddCommand(newDatabaseBackupDownloadCmd(b))
	return cmd
}

func newDatabaseBackupCreateCmd(b Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create [name]",
		Short: "Create a backup now",
		Long: `Triggers the backup cron job immediately.

Examples:
  nvoi database backup create db --type postgres`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind, err := resolveManagedKind(cmd, "database")
			if err != nil {
				return err
			}
			return b.BackupCreate(cmd.Context(), args[0], kind)
		},
	}
	cmd.Flags().String("type", "", "database type (postgres)")
	return cmd
}

func newDatabaseBackupListCmd(b Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list [name]",
		Short: "List backup artifacts",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind, err := resolveManagedKind(cmd, "database")
			if err != nil {
				return err
			}
			backupStorage, _ := cmd.Flags().GetString("backup-storage")
			return b.BackupList(cmd.Context(), args[0], kind, backupStorage)
		},
	}
	cmd.Flags().String("type", "", "database type (postgres)")
	cmd.Flags().String("backup-storage", "", "storage name for backups (default: {name}-backups)")
	return cmd
}

func newDatabaseBackupDownloadCmd(b Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "download [name] [artifact]",
		Short: "Download a backup artifact",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind, err := resolveManagedKind(cmd, "database")
			if err != nil {
				return err
			}
			backupStorage, _ := cmd.Flags().GetString("backup-storage")
			return b.BackupDownload(cmd.Context(), args[0], kind, backupStorage, args[1])
		},
	}
	cmd.Flags().String("type", "", "database type (postgres)")
	cmd.Flags().String("backup-storage", "", "storage name for backups (default: {name}-backups)")
	return cmd
}
