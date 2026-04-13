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
	cmd.PersistentFlags().StringVar(&dbName, "name", "", "database name (defaults to first)")

	cmd.AddCommand(newDatabaseBackupCmd(dc, &dbName))
	cmd.AddCommand(newDatabaseSQLCmd(dc, &dbName))
	return cmd
}

// resolveDB loads config to find available database names, then resolves.
func resolveDB(cmd *cobra.Command, flag *string) string {
	cfg, _ := LoadConfig(cmd)
	return utils.ResolveDBName(*flag, dbNames(cfg))
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

func newDatabaseBackupCmd(dc *config.DeployContext, dbName *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Manage database backups",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "now",
		Short: "Trigger a backup immediately",
		RunE: func(cmd *cobra.Command, args []string) error {
			name := resolveDB(cmd, dbName)
			cronName := name + "-db-backup"
			return app.CronRun(cmd.Context(), app.CronRunRequest{
				Cluster: dc.Cluster,
				Name:    cronName,
			})
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List backups in bucket",
		RunE: func(cmd *cobra.Command, args []string) error {
			name := resolveDB(cmd, dbName)
			return DatabaseBackupList(cmd, dc, name)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "download <backup-name>",
		Short: "Download a backup file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := resolveDB(cmd, dbName)
			outFile, _ := cmd.Flags().GetString("file")
			return DatabaseBackupDownload(cmd, dc, name, args[0], outFile)
		},
	})
	cmd.Commands()[2].Flags().StringP("file", "f", "", "output file (default: stdout)")
	return cmd
}

func newDatabaseSQLCmd(dc *config.DeployContext, dbName *string) *cobra.Command {
	return &cobra.Command{
		Use:   "sql <query>",
		Short: "Run SQL against the database",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := resolveDB(cmd, dbName)
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
