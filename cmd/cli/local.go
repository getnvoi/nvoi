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
	"github.com/getnvoi/nvoi/internal/render"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/infra"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// localBackend runs commands directly — no agent. Used for first deploy
// (bootstrap) before the agent is installed on the master.
type localBackend struct {
	dc  *config.DeployContext
	cfg *config.AppConfig
	out app.Output
}

func newLocalBackend(ctx context.Context, out app.Output, cfg *config.AppConfig) (*localBackend, error) {
	// Resolve env-dependent values here — cmd/ is the os.Getenv boundary.
	sshKey, _ := resolveAgentSSHKey()
	gitUsername, gitToken := resolveAgentGitAuth()

	dc, err := agent.BuildDeployContext(ctx, out, cfg, agent.AgentOpts{
		SSHKey:      sshKey,
		GitUsername: gitUsername,
		GitToken:    gitToken,
	})
	if err != nil {
		return nil, err
	}
	return &localBackend{dc: dc, cfg: cfg, out: out}, nil
}

// ── Backend methods ─────────────────────────────────────────────────────────

func (b *localBackend) Deploy(ctx context.Context) error {
	// Bootstrap: provision master, install agent, hand off.
	// The laptop's only job is getting the agent running. The agent does the real deploy.

	// 1. Provision master server.
	b.out.Command("bootstrap", "provision", "master")
	sshKey := b.dc.Cluster.SSHKey
	connectSSH := func(ctx context.Context, addr string) (utils.SSHClient, error) {
		return infra.ConnectSSH(ctx, addr, utils.DefaultUser, sshKey)
	}
	if _, err := app.ComputeSet(ctx, app.ComputeSetRequest{
		Cluster: b.dc.Cluster, Output: b.out, ConnectSSH: connectSSH,
		Name: "master", ServerType: b.cfg.Servers["master"].Type,
		Region: b.cfg.Servers["master"].Region, Worker: false,
		DiskGB: b.cfg.Servers["master"].Disk,
	}); err != nil {
		return fmt.Errorf("provision master: %w", err)
	}

	// 2. Find master IP, SSH in.
	prov, err := b.dc.Cluster.Compute()
	if err != nil {
		return err
	}
	names, err := b.dc.Cluster.Names()
	if err != nil {
		return err
	}
	master, err := app.FindMaster(ctx, prov, names)
	if err != nil {
		return fmt.Errorf("find master after provisioning: %w", err)
	}

	ssh, err := infra.ConnectSSH(ctx, master.IPv4+":22", utils.DefaultUser, sshKey)
	if err != nil {
		return fmt.Errorf("SSH to master: %w", err)
	}
	defer ssh.Close()

	// 3. Install agent.
	b.out.Command("agent", "install", b.cfg.App)
	configData, _ := config.MarshalAppConfig(b.cfg)
	envData, _ := os.ReadFile(".env")
	if err := infra.InstallAgent(ctx, ssh, b.cfg.App, b.cfg.Env, configData, envData); err != nil {
		return fmt.Errorf("agent install: %w", err)
	}
	b.out.Success("agent installed")

	// 4. Connect to agent and delegate the full deploy.
	b.out.Command("bootstrap", "handoff", "agent")
	ab, cleanup, err := connectToAgent(ctx, b.out, b.cfg, "nvoi.yaml")
	if err != nil {
		return fmt.Errorf("connect to agent after install: %w", err)
	}
	defer cleanup()

	return ab.Deploy(ctx)
}

func (b *localBackend) Teardown(ctx context.Context, deleteVolumes, deleteStorage bool) error {
	return core.Teardown(ctx, b.dc, b.cfg, deleteVolumes, deleteStorage)
}

func (b *localBackend) Describe(ctx context.Context, jsonOutput bool) error {
	req := app.DescribeRequest{
		Cluster: b.dc.Cluster, Output: b.out,
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
		Cluster: b.dc.Cluster, Output: b.out, Service: opts.Service,
		Follow: opts.Follow, Tail: opts.Tail, Since: opts.Since,
		Previous: opts.Previous, Timestamps: opts.Timestamps,
	})
}

func (b *localBackend) Exec(ctx context.Context, service string, command []string) error {
	return app.Exec(ctx, app.ExecRequest{
		Cluster: b.dc.Cluster, Output: b.out, Service: service, Command: command,
	})
}

func (b *localBackend) SSH(ctx context.Context, command []string) error {
	connectFn := b.dc.ConnectSSH
	if connectFn == nil {
		connectFn = func(ctx context.Context, addr string) (utils.SSHClient, error) {
			return infra.ConnectSSH(ctx, addr, utils.DefaultUser, b.dc.Cluster.SSHKey)
		}
	}
	ssh, err := connectFn(ctx, b.dc.Cluster.MasterIP+":22")
	if err != nil {
		return err
	}
	defer ssh.Close()
	return app.SSH(ctx, app.SSHRequest{
		Cluster: b.dc.Cluster, Output: b.out, Command: command,
		RunStreamMaster: ssh.RunStream,
	})
}

func (b *localBackend) CronRun(ctx context.Context, name string) error {
	return app.CronRun(ctx, app.CronRunRequest{Cluster: b.dc.Cluster, Output: b.out, Name: name})
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
		Cluster: b.dc.Cluster, Output: b.out, DBName: name,
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
		Cluster: b.dc.Cluster, Output: b.out, DBName: name, Key: key,
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
		Cluster: b.dc.Cluster, Output: b.out, DBName: name, Engine: engine, Query: query,
	})
	if err != nil {
		return err
	}
	fmt.Print(output)
	return nil
}
