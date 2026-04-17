package reconcile

import (
	"context"

	"github.com/getnvoi/nvoi/internal/config"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/provider"
)

// Firewall reconciles the desired per-role firewall set. A firewall is
// created/updated for each role that has at least one server in the config.
//
// Orphan cleanup is NOT done here — it's deferred to FirewallRemoveOrphans,
// which runs after ServersRemoveOrphans in Deploy so any orphan firewall has
// already been detached from its server by DeleteServer. The Hetzner
// DeleteFirewall contract requires "no attached resources" and fails with
// resource_in_use otherwise; that's why the two phases are split.
func Firewall(ctx context.Context, dc *config.DeployContext, _ *config.LiveState, cfg *config.AppConfig) error {
	if len(cfg.Firewall) == 0 {
		return nil
	}

	// Resolve user-facing rules (80/443 from "default" preset + custom rules)
	publicRules, err := provider.ResolveFirewallArgs(ctx, cfg.Firewall)
	if err != nil {
		return err
	}

	masters, workers := SplitServers(cfg.Servers)

	// Master gets public ports (80, 443) — ingress runs here.
	if len(masters) > 0 {
		if err := app.FirewallSet(ctx, app.FirewallSetRequest{
			Cluster:    dc.Cluster,
			Name:       cfg.MasterFirewall,
			AllowedIPs: publicRules,
		}); err != nil {
			return err
		}
	}

	// Workers get base rules only (SSH + internal) — no public ports.
	// Skip entirely when the config has no workers — no firewall, no orphan.
	if len(workers) > 0 {
		if err := app.FirewallSet(ctx, app.FirewallSetRequest{
			Cluster:    dc.Cluster,
			Name:       cfg.WorkerFirewall,
			AllowedIPs: nil,
		}); err != nil {
			return err
		}
	}

	return nil
}

// FirewallRemoveOrphans deletes per-role firewalls whose role is no longer
// represented in the config (e.g. the worker firewall after the last worker
// has been removed).
//
// Must run after ServersRemoveOrphans — DeleteServer detaches the firewall
// as part of its teardown contract, so by the time we reach this step any
// orphan firewall has zero attached resources and DeleteFirewall succeeds.
// Running it earlier (the previous inline placement) meant Hetzner correctly
// rejected the delete with resource_in_use and nothing ever retried.
//
// Best-effort: app.FirewallRemoveOrphans logs per-firewall failures as
// warnings; a straggler on this pass gets caught on the next deploy.
func FirewallRemoveOrphans(ctx context.Context, dc *config.DeployContext, _ *config.LiveState, cfg *config.AppConfig) error {
	if len(cfg.Firewall) == 0 {
		return nil
	}
	names, err := dc.Cluster.Names()
	if err != nil {
		return err
	}
	app.FirewallRemoveOrphans(ctx, app.FirewallRemoveOrphansRequest{
		Cluster: dc.Cluster,
		Prefix:  names.Base() + "-",
		Desired: desiredFirewalls(cfg),
	})
	return nil
}

// desiredFirewalls returns the set of per-role firewall names the current
// config wants to exist. Shared between Firewall and FirewallRemoveOrphans
// so the two phases can't drift on what's considered desired.
func desiredFirewalls(cfg *config.AppConfig) map[string]bool {
	masters, workers := SplitServers(cfg.Servers)
	desired := map[string]bool{}
	if len(masters) > 0 {
		desired[cfg.MasterFirewall] = true
	}
	if len(workers) > 0 {
		desired[cfg.WorkerFirewall] = true
	}
	return desired
}
