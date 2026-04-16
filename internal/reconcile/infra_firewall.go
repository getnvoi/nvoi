package reconcile

import (
	"context"

	"github.com/getnvoi/nvoi/internal/config"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/provider"
)

func Firewall(ctx context.Context, dc *config.DeployContext, live *config.LiveState, cfg *config.AppConfig) error {
	if len(cfg.Firewall) == 0 {
		return nil
	}

	names, err := dc.Cluster.Names()
	if err != nil {
		return err
	}

	// Resolve user-facing rules (80/443 from "default" preset + custom rules)
	publicRules, err := provider.ResolveFirewallArgs(ctx, cfg.Firewall)
	if err != nil {
		return err
	}

	// Master gets public ports (80, 443) — ingress runs here
	if err := app.FirewallSet(ctx, app.FirewallSetRequest{
		Cluster:    dc.Cluster,
		Name:       cfg.MasterFirewall,
		AllowedIPs: publicRules,
	}); err != nil {
		return err
	}

	// Workers get base rules only (SSH + internal) — no public ports
	if err := app.FirewallSet(ctx, app.FirewallSetRequest{
		Cluster:    dc.Cluster,
		Name:       cfg.WorkerFirewall,
		AllowedIPs: nil,
	}); err != nil {
		return err
	}

	// Orphan detection — same function teardown uses with desired=nil
	app.FirewallRemoveOrphans(ctx, app.FirewallRemoveOrphansRequest{
		Cluster: dc.Cluster,
		Prefix:  names.Base() + "-",
		Desired: map[string]bool{
			cfg.MasterFirewall: true,
			cfg.WorkerFirewall: true,
		},
	})

	return nil
}
