package reconcile

import (
	"context"

	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/utils"
)

func DNS(ctx context.Context, dc *DeployContext, live *LiveState, cfg *AppConfig) error {
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
				_ = app.DNSDelete(ctx, app.DNSDeleteRequest{
					Cluster: dc.Cluster, DNS: dc.DNS,
					Service: svcName, Domains: domains,
				})
			}
		}
	}
	return nil
}
