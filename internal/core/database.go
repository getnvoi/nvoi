package core

import (
	"fmt"
	"io"
	"os"

	"github.com/getnvoi/nvoi/internal/config"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/utils"
	"github.com/spf13/cobra"
)

func NewDatabaseCmd(dc *config.DeployContext, cfg **config.AppConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "database",
		Aliases: []string{"db"},
		Short:   "Database operations",
	}

	var dbName string
	cmd.PersistentFlags().StringVar(&dbName, "name", "", "database name (required when multiple databases configured)")

	resolve := func() (string, error) {
		return utils.ResolveDBName(dbName, (*cfg).DatabaseNames())
	}

	cmd.AddCommand(newDatabaseBackupCmd(dc, resolve))
	cmd.AddCommand(newDatabaseSQLCmd(dc, resolve))
	return cmd
}

func newDatabaseBackupCmd(dc *config.DeployContext, resolve func() (string, error)) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Manage database backups",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "now",
		Short: "Trigger a backup immediately",
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := resolve()
			if err != nil {
				return err
			}
			return app.CronRun(cmd.Context(), app.CronRunRequest{
				Cluster: dc.Cluster,
				Name:    name + "-db-backup",
			})
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List backups in bucket",
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := resolve()
			if err != nil {
				return err
			}
			return DatabaseBackupList(cmd, dc, name)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "download <backup-name>",
		Short: "Download a backup file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := resolve()
			if err != nil {
				return err
			}
			outFile, _ := cmd.Flags().GetString("file")
			return DatabaseBackupDownload(cmd, dc, name, args[0], outFile)
		},
	})
	cmd.Commands()[2].Flags().StringP("file", "f", "", "output file (default: stdout)")
	return cmd
}

func newDatabaseSQLCmd(dc *config.DeployContext, resolve func() (string, error)) *cobra.Command {
	return &cobra.Command{
		Use:   "sql <query>",
		Short: "Run SQL against the database",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := resolve()
			if err != nil {
				return err
			}
			return DatabaseSQL(cmd, dc, name, args[0])
		},
	}
}

func DatabaseBackupList(cmd *cobra.Command, dc *config.DeployContext, name string) error {
	entries, err := app.DatabaseBackupList(cmd.Context(), app.DatabaseBackupListRequest{
		Cluster: dc.Cluster,
		DBName:  name,
	})
	if err != nil {
		return err
	}

	out := dc.Cluster.Log()
	out.Command("database", "backup list", name)
	if len(entries) == 0 {
		out.Info("no backups found")
		return nil
	}
	for _, e := range entries {
		out.Info(fmt.Sprintf("%s  %s  %d bytes", e.LastModified, e.Key, e.Size))
	}
	return nil
}

func DatabaseBackupDownload(cmd *cobra.Command, dc *config.DeployContext, name, backupKey, outFile string) error {
	body, _, err := app.DatabaseBackupDownload(cmd.Context(), app.DatabaseBackupDownloadRequest{
		Cluster: dc.Cluster,
		DBName:  name,
		Key:     backupKey,
	})
	if err != nil {
		return err
	}
	defer body.Close()

	out := dc.Cluster.Log()
	out.Command("database", "backup download", backupKey)

	var w io.Writer = os.Stdout
	if outFile != "" {
		f, err := os.Create(outFile)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}

	n, err := io.Copy(w, body)
	if err != nil {
		return err
	}
	if outFile != "" {
		out.Success(fmt.Sprintf("downloaded %s (%d bytes)", outFile, n))
	}
	return nil
}

// DatabaseSQL prints raw query output directly — not through Output.
// psql/mysql table formatting breaks if wrapped in TUI chrome or JSONL events.
// This means --json has no effect on db sql output. Intentional.
func DatabaseSQL(cmd *cobra.Command, dc *config.DeployContext, name, query string) error {
	output, err := app.DatabaseSQL(cmd.Context(), app.DatabaseSQLRequest{
		Cluster: dc.Cluster,
		DBName:  name,
		Query:   query,
	})
	if err != nil {
		return err
	}
	fmt.Print(output)
	return nil
}
