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
	allowed, err := provider.ResolveFirewallArgs(ctx, cfg.Firewall)
	if err != nil {
		return err
	}
	return app.FirewallSet(ctx, app.FirewallSetRequest{Cluster: dc.Cluster, AllowedIPs: allowed})
}
