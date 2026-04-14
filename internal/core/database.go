package core

import (
	"fmt"
	"io"
	"os"

	"github.com/getnvoi/nvoi/internal/config"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/spf13/cobra"
)

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
func DatabaseSQL(cmd *cobra.Command, dc *config.DeployContext, name, engine, query string) error {
	output, err := app.DatabaseSQL(cmd.Context(), app.DatabaseSQLRequest{
		Cluster: dc.Cluster,
		DBName:  name,
		Engine:  engine,
		Query:   query,
	})
	if err != nil {
		return err
	}
	fmt.Print(output)
	return nil
}
