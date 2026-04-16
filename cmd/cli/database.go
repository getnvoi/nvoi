package main

import (
	"fmt"
	"io"
	"os"

	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/utils"
	"github.com/spf13/cobra"
)

func newDatabaseCmd(rt *runtime) *cobra.Command {
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
			name, err := resolveDBName(rt, dbName)
			if err != nil {
				return err
			}
			return app.CronRun(cmd.Context(), app.CronRunRequest{
				Cluster: rt.dc.Cluster,
				Name:    utils.DatabaseBackupCronName(name),
			})
		},
	})

	backupCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List backups in bucket",
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := resolveDBName(rt, dbName)
			if err != nil {
				return err
			}
			entries, err := app.DatabaseBackupList(cmd.Context(), app.DatabaseBackupListRequest{
				Cluster: rt.dc.Cluster,
				DBName:  name,
			})
			if err != nil {
				return err
			}
			rt.out.Command("database", "backup list", name)
			if len(entries) == 0 {
				rt.out.Info("no backups found")
				return nil
			}
			for _, e := range entries {
				rt.out.Info(fmt.Sprintf("%s  %s  %d bytes", e.LastModified, e.Key, e.Size))
			}
			return nil
		},
	})

	dlCmd := &cobra.Command{
		Use:   "download <backup-name>",
		Short: "Download a backup file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := resolveDBName(rt, dbName)
			if err != nil {
				return err
			}
			outFile, _ := cmd.Flags().GetString("file")
			body, _, err := app.DatabaseBackupDownload(cmd.Context(), app.DatabaseBackupDownloadRequest{
				Cluster: rt.dc.Cluster,
				DBName:  name,
				Key:     args[0],
			})
			if err != nil {
				return err
			}
			defer body.Close()
			rt.out.Command("database", "backup download", args[0])
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
				rt.out.Success(fmt.Sprintf("downloaded %s (%d bytes)", outFile, n))
			}
			return nil
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
			name, err := resolveDBName(rt, dbName)
			if err != nil {
				return err
			}
			engine, _ := cmd.Flags().GetString("kind")
			if engine == "" {
				if db, ok := rt.cfg.Database[name]; ok {
					engine = db.Kind
				}
			}
			if engine == "" {
				return fmt.Errorf("--kind is required (postgres or mysql)")
			}
			output, err := app.DatabaseSQL(cmd.Context(), app.DatabaseSQLRequest{
				Cluster: rt.dc.Cluster,
				DBName:  name,
				Engine:  engine,
				Query:   args[0],
			})
			if err != nil {
				return err
			}
			fmt.Print(output)
			return nil
		},
	}
	sqlCmd.Flags().String("kind", "", "database engine (postgres or mysql) — auto-detected from config")
	cmd.AddCommand(sqlCmd)

	return cmd
}

func resolveDBName(rt *runtime, dbName string) (string, error) {
	return utils.ResolveDBName(dbName, rt.cfg.DatabaseNames())
}
