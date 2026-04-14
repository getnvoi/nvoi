package main

import (
	"github.com/spf13/cobra"
)

func newDatabaseCmd(m *mode) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "database",
		Aliases: []string{"db"},
		Short:   "Database operations",
	}

	var dbName string
	cmd.PersistentFlags().StringVar(&dbName, "name", "", "database name (required when multiple databases configured)")

	// ── backup ──────────────────────────────────────────────────────────────

	backupCmd := &cobra.Command{Use: "backup", Short: "Manage database backups"}

	backupCmd.AddCommand(&cobra.Command{
		Use:   "now",
		Short: "Trigger a backup immediately",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Backup "now" is a CronRun on the backup cron job.
			// The backend resolves dbName; we append the convention suffix.
			// TODO: this leaks the "-db-backup" naming convention into the command.
			// Move to backend when both backends support a DatabaseBackupNow method.
			name := dbName
			if name == "" {
				name = "main" // will be resolved by backend in the future
			}
			return m.backend.CronRun(cmd.Context(), name+"-db-backup")
		},
	})

	backupCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List backups in bucket",
		RunE: func(cmd *cobra.Command, args []string) error {
			return m.backend.DatabaseBackupList(cmd.Context(), dbName)
		},
	})

	dlCmd := &cobra.Command{
		Use:   "download <backup-name>",
		Short: "Download a backup file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			outFile, _ := cmd.Flags().GetString("file")
			return m.backend.DatabaseBackupDownload(cmd.Context(), dbName, args[0], outFile)
		},
	}
	dlCmd.Flags().StringP("file", "f", "", "output file (default: stdout)")
	backupCmd.AddCommand(dlCmd)

	cmd.AddCommand(backupCmd)

	// ── sql ─────────────────────────────────────────────────────────────────

	sqlCmd := &cobra.Command{
		Use:   "sql <query>",
		Short: "Run SQL against the database",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			engine, _ := cmd.Flags().GetString("kind")
			return m.backend.DatabaseSQL(cmd.Context(), dbName, engine, args[0])
		},
	}
	sqlCmd.Flags().String("kind", "", "database engine (postgres or mysql) — auto-detected in local mode")
	cmd.AddCommand(sqlCmd)

	return cmd
}
