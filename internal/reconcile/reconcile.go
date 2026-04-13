package reconcile

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/internal/packages"
	"github.com/spf13/viper"
)

// Deploy reconciles live infrastructure to match the YAML config.
func Deploy(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig, v *viper.Viper) error {
	if err := ValidateConfig(cfg); err != nil {
		return err
	}
	if err := packages.ValidateAll(cfg); err != nil {
		return err
	}

	live, err := DescribeLive(ctx, dc)
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

	if err := Firewall(ctx, dc, live, cfg); err != nil {
		return err
	}
	if err := Volumes(ctx, dc, live, cfg); err != nil {
		return err
	}
	if err := Build(ctx, dc, cfg); err != nil {
		return err
	}
	if err := Secrets(ctx, dc, live, cfg, v); err != nil {
		return err
	}

	// Packages (database, etc.) — after volumes/secrets, before services.
	// Returns env vars to inject into all app services and crons.
	packageEnvVars, err := packages.ReconcileAll(ctx, dc, cfg)
	if err != nil {
		return err
	}

	if err := Storage(ctx, dc, live, cfg); err != nil {
		return err
	}
	if err := Services(ctx, dc, live, cfg, packageEnvVars); err != nil {
		return err
	}
	if err := Crons(ctx, dc, live, cfg, packageEnvVars); err != nil {
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
