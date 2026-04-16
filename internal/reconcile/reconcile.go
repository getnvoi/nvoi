package reconcile

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/internal/packages"
	"github.com/getnvoi/nvoi/pkg/kube"
)

// ErrDeployInterrupted is returned when a deploy is cancelled mid-reconcile.
// The error message includes the last completed step so the user knows where
// it stopped. The next `nvoi deploy` picks up from there — idempotent by design.
var ErrDeployInterrupted = fmt.Errorf("deploy interrupted")

// checkCancel returns ErrDeployInterrupted if the context has been cancelled,
// annotated with the last completed step.
func checkCancel(ctx context.Context, lastStep string) error {
	if ctx.Err() != nil {
		return fmt.Errorf("%w after %s: %v", ErrDeployInterrupted, lastStep, ctx.Err())
	}
	return nil
}

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
	if err := checkCancel(ctx, "servers"); err != nil {
		return err
	}

	// Bootstrap path: SSH + tunneled KubeClient. Agent path: Kube already set, no SSH.
	if dc.Cluster.Kube == nil {
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

		kubeClient, err := kube.NewTunneled(ctx, ssh)
		if err != nil {
			return fmt.Errorf("kube client via SSH tunnel: %w", err)
		}
		dc.Cluster.Kube = kubeClient
	}

	if err := Firewall(ctx, dc, live, cfg); err != nil {
		return err
	}
	if err := checkCancel(ctx, "firewall"); err != nil {
		return err
	}
	if err := Volumes(ctx, dc, live, cfg); err != nil {
		return err
	}
	if err := checkCancel(ctx, "volumes"); err != nil {
		return err
	}
	if err := Build(ctx, dc, cfg); err != nil {
		return err
	}
	if err := checkCancel(ctx, "build"); err != nil {
		return err
	}
	secretValues, err := Secrets(ctx, dc, live, cfg)
	if err != nil {
		return err
	}
	if err := checkCancel(ctx, "secrets"); err != nil {
		return err
	}

	// Packages (database, etc.) — after volumes/secrets, before services.
	// Returns env vars available as $VAR resolution sources.
	packageEnvVars, err := packages.ReconcileAll(ctx, dc, cfg)
	if err != nil {
		return err
	}
	if err := checkCancel(ctx, "packages"); err != nil {
		return err
	}

	storageCreds, err := Storage(ctx, dc, live, cfg)
	if err != nil {
		return err
	}
	if err := checkCancel(ctx, "storage"); err != nil {
		return err
	}

	// Build unified sources for $VAR resolution and per-service secret storage.
	sources := mergeSources(secretValues, packageEnvVars, storageCreds)

	if err := Services(ctx, dc, live, cfg, sources); err != nil {
		return err
	}
	if err := checkCancel(ctx, "services"); err != nil {
		return err
	}
	if err := Crons(ctx, dc, live, cfg, sources); err != nil {
		return err
	}
	if err := checkCancel(ctx, "crons"); err != nil {
		return err
	}

	// Workloads have moved. Now safe to drain + delete orphan servers.
	if err := ServersRemoveOrphans(ctx, dc, live, cfg); err != nil {
		return err
	}
	if err := checkCancel(ctx, "orphan removal"); err != nil {
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
