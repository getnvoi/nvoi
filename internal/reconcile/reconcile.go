package reconcile

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/internal/packages"
	"github.com/getnvoi/nvoi/pkg/kube"
)

// Deploy reconciles live infrastructure to match the YAML config.
func Deploy(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig) error {
	if err := ValidateConfig(cfg); err != nil {
		return err
	}
	if err := cfg.Resolve(); err != nil {
		return err
	}

	live, err := DescribeLive(ctx, dc, cfg)
	if err != nil {
		return err
	}

	// Create desired servers. Orphans are NOT removed yet — workloads
	// must move to new nodes first (zero-downtime server replacement).
	if err := ServersAdd(ctx, dc, live, cfg); err != nil {
		return err
	}

	// Master is now guaranteed to exist. Establish a single SSH connection
	// for all remaining operations.
	master, _, _, err := dc.Cluster.Master(ctx)
	if err != nil {
		return fmt.Errorf("resolve master after server setup: %w", err)
	}
	ssh, err := dc.Cluster.Connect(ctx, master.IPv4+":22")
	if err != nil {
		return fmt.Errorf("establish master SSH: %w", err)
	}
	defer ssh.Close()
	dc.Cluster.MasterSSH = ssh

	// Create k8s client through SSH tunnel if not already set (agent sets it directly).
	if dc.Cluster.Kube == nil {
		kubeClient, err := kube.NewTunneled(ctx, ssh)
		if err != nil {
			return fmt.Errorf("kube client via SSH tunnel: %w", err)
		}
		dc.Cluster.Kube = kubeClient
	}

	if err := Firewall(ctx, dc, live, cfg); err != nil {
		return err
	}
	if err := Volumes(ctx, dc, live, cfg); err != nil {
		return err
	}
	if err := Build(ctx, dc, cfg); err != nil {
		return err
	}
	secretValues, err := Secrets(ctx, dc, live, cfg)
	if err != nil {
		return err
	}

	// Packages (database, etc.) — after volumes/secrets, before services.
	// Returns env vars available as $VAR resolution sources.
	packageEnvVars, err := packages.ReconcileAll(ctx, dc, cfg)
	if err != nil {
		return err
	}

	storageCreds, err := Storage(ctx, dc, live, cfg)
	if err != nil {
		return err
	}

	// Build unified sources for $VAR resolution and per-service secret storage.
	sources := mergeSources(secretValues, packageEnvVars, storageCreds)

	if err := Services(ctx, dc, live, cfg, sources); err != nil {
		return err
	}
	if err := Crons(ctx, dc, live, cfg, sources); err != nil {
		return err
	}

	// Workloads have moved. Now safe to drain + delete orphan servers.
	if err := ServersRemoveOrphans(ctx, dc, live, cfg); err != nil {
		return err
	}

	if err := DNS(ctx, dc, live, cfg); err != nil {
		return err
	}

	// Verify DNS propagation before ACME — warn if domains don't resolve yet.
	if len(cfg.Domains) > 0 {
		verifyDNSPropagation(ctx, dc, cfg)
	}

	return Ingress(ctx, dc, live, cfg)
}
