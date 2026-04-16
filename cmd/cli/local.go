package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/getnvoi/nvoi/internal/agent"
	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/internal/core"
	"github.com/getnvoi/nvoi/internal/reconcile"
	"github.com/getnvoi/nvoi/internal/render"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// localBackend runs commands directly — no agent. Used for first deploy
// (bootstrap) before the agent is installed on the master.
type localBackend struct {
	dc  *config.DeployContext
	cfg *config.AppConfig
	out app.Output
}

func newLocalBackend(ctx context.Context, out app.Output, cfg *config.AppConfig) *localBackend {
	return &localBackend{
		dc:  agent.BuildDeployContext(ctx, out, cfg),
		cfg: cfg,
		out: out,
	}
}

// ── Backend methods ─────────────────────────────────────────────────────────

func (b *localBackend) Deploy(ctx context.Context) error {
	return reconcile.Deploy(ctx, b.dc, b.cfg)
}

func (b *localBackend) Teardown(ctx context.Context, deleteVolumes, deleteStorage bool) error {
	return core.Teardown(ctx, b.dc, b.cfg, deleteVolumes, deleteStorage)
}

func (b *localBackend) Describe(ctx context.Context, jsonOutput bool) error {
	req := app.DescribeRequest{
		Cluster:        b.dc.Cluster,
		StorageNames:   b.cfg.StorageNames(),
		ServiceSecrets: b.cfg.ServiceSecrets(),
	}
	if jsonOutput {
		raw, err := app.DescribeJSON(ctx, req)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(raw)
	}
	res, err := app.Describe(ctx, req)
	if err != nil {
		return err
	}
	render.RenderDescribe(res)
	return nil
}

func (b *localBackend) Resources(ctx context.Context, jsonOutput bool) error {
	groups, err := app.Resources(ctx, app.ResourcesRequest{
		Compute: app.ProviderRef{Name: b.dc.Cluster.Provider, Creds: b.dc.Cluster.Credentials},
		DNS:     b.dc.DNS,
		Storage: b.dc.Storage,
	})
	if err != nil {
		return err
	}
	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(groups)
	}
	render.RenderResources(groups)
	return nil
}

func (b *localBackend) Logs(ctx context.Context, opts LogsOpts) error {
	return app.Logs(ctx, app.LogsRequest{
		Cluster: b.dc.Cluster, Service: opts.Service,
		Follow: opts.Follow, Tail: opts.Tail, Since: opts.Since,
		Previous: opts.Previous, Timestamps: opts.Timestamps,
	})
}

func (b *localBackend) Exec(ctx context.Context, service string, command []string) error {
	return app.Exec(ctx, app.ExecRequest{
		Cluster: b.dc.Cluster, Service: service, Command: command,
	})
}

func (b *localBackend) SSH(ctx context.Context, command []string) error {
	return app.SSH(ctx, app.SSHRequest{Cluster: b.dc.Cluster, Command: command})
}

func (b *localBackend) CronRun(ctx context.Context, name string) error {
	return app.CronRun(ctx, app.CronRunRequest{Cluster: b.dc.Cluster, Name: name})
}

// ── Database ────────────────────────────────────────────────────────────────

func (b *localBackend) resolveDB(dbName string) (string, error) {
	return utils.ResolveDBName(dbName, b.cfg.DatabaseNames())
}

func (b *localBackend) DatabaseBackupList(ctx context.Context, dbName string) error {
	name, err := b.resolveDB(dbName)
	if err != nil {
		return err
	}
	entries, err := app.DatabaseBackupList(ctx, app.DatabaseBackupListRequest{
		Cluster: b.dc.Cluster, DBName: name,
	})
	if err != nil {
		return err
	}
	b.out.Command("database", "backup list", name)
	if len(entries) == 0 {
		b.out.Info("no backups found")
		return nil
	}
	for _, e := range entries {
		b.out.Info(fmt.Sprintf("%s  %s  %d bytes", e.LastModified, e.Key, e.Size))
	}
	return nil
}

func (b *localBackend) DatabaseBackupDownload(ctx context.Context, dbName, key, outFile string) error {
	name, err := b.resolveDB(dbName)
	if err != nil {
		return err
	}
	body, _, err := app.DatabaseBackupDownload(ctx, app.DatabaseBackupDownloadRequest{
		Cluster: b.dc.Cluster, DBName: name, Key: key,
	})
	if err != nil {
		return err
	}
	defer body.Close()
	b.out.Command("database", "backup download", key)
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
		b.out.Success(fmt.Sprintf("downloaded %s (%d bytes)", outFile, n))
	}
	return nil
}

func (b *localBackend) DatabaseSQL(ctx context.Context, dbName, engine, query string) error {
	name, err := b.resolveDB(dbName)
	if err != nil {
		return err
	}
	if engine == "" {
		if db, ok := b.cfg.Database[name]; ok {
			engine = db.Kind
		}
	}
	if engine == "" {
		return fmt.Errorf("--kind is required (postgres or mysql)")
	}
	output, err := app.DatabaseSQL(ctx, app.DatabaseSQLRequest{
		Cluster: b.dc.Cluster, DBName: name, Engine: engine, Query: query,
	})
	if err != nil {
		return err
	}
	fmt.Print(output)
	return nil
}
