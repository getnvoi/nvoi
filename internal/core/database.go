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

func NewDatabaseCmd(dc *config.DeployContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "database",
		Aliases: []string{"db"},
		Short:   "Database operations",
	}

	var dbName string
	var cfg *config.AppConfig
	cmd.PersistentFlags().StringVar(&dbName, "name", "", "database name (defaults to first)")

	// Parse config once for all database subcommands.
	// Chain with parent's PersistentPreRunE (which populates dc via BuildContext).
	cmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if parent := cmd.Root().PersistentPreRunE; parent != nil {
			if err := parent(cmd, args); err != nil {
				return err
			}
		}
		c, err := LoadConfig(cmd)
		if err != nil {
			return err
		}
		cfg = c
		return nil
	}

	resolve := func() string {
		return utils.ResolveDBName(dbName, dbNames(cfg))
	}

	cmd.AddCommand(newDatabaseBackupCmd(dc, resolve))
	cmd.AddCommand(newDatabaseSQLCmd(dc, resolve))
	return cmd
}

func dbNames(cfg *config.AppConfig) []string {
	if cfg == nil {
		return nil
	}
	names := make([]string, 0, len(cfg.Database))
	for n := range cfg.Database {
		names = append(names, n)
	}
	return names
}

func newDatabaseBackupCmd(dc *config.DeployContext, resolve func() string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Manage database backups",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "now",
		Short: "Trigger a backup immediately",
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.CronRun(cmd.Context(), app.CronRunRequest{
				Cluster: dc.Cluster,
				Name:    resolve() + "-db-backup",
			})
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List backups in bucket",
		RunE: func(cmd *cobra.Command, args []string) error {
			return DatabaseBackupList(cmd, dc, resolve())
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "download <backup-name>",
		Short: "Download a backup file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			outFile, _ := cmd.Flags().GetString("file")
			return DatabaseBackupDownload(cmd, dc, resolve(), args[0], outFile)
		},
	})
	cmd.Commands()[2].Flags().StringP("file", "f", "", "output file (default: stdout)")
	return cmd
}

func newDatabaseSQLCmd(dc *config.DeployContext, resolve func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "sql <query>",
		Short: "Run SQL against the database",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return DatabaseSQL(cmd, dc, resolve(), args[0])
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
