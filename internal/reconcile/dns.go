package reconcile

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/internal/config"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/utils"
)

func DNS(ctx context.Context, dc *config.DeployContext, live *config.LiveState, cfg *config.AppConfig) error {
	for _, svcName := range utils.SortedKeys(cfg.Domains) {
		if err := app.DNSSet(ctx, app.DNSSetRequest{
			Cluster: dc.Cluster, DNS: dc.DNS,
			Service: svcName, Domains: cfg.Domains[svcName],
		}); err != nil {
			return err
		}
	}
	if live != nil {
		for svcName, domains := range live.Domains {
			if _, ok := cfg.Domains[svcName]; !ok {
				if err := app.DNSDelete(ctx, app.DNSDeleteRequest{
					Cluster: dc.Cluster, DNS: dc.DNS,
					Service: svcName, Domains: domains,
				}); err != nil {
					dc.Cluster.Log().Warning(fmt.Sprintf("orphan DNS for %s not removed: %s", svcName, err))
				}
			}
		}
	}
	return nil
}
