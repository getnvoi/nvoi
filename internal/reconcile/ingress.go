package reconcile

import (
	"context"

	"github.com/getnvoi/nvoi/internal/config"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/utils"
)

func Ingress(ctx context.Context, dc *config.DeployContext, live *config.LiveState, cfg *config.AppConfig) error {
	for _, svcName := range utils.SortedKeys(cfg.Domains) {
		var healthPath string
		if svc, ok := cfg.Services[svcName]; ok {
			healthPath = svc.Health
		}
		if err := app.IngressSet(ctx, app.IngressSetRequest{
			Cluster:    dc.Cluster,
			Route:      app.IngressRouteArg{Service: svcName, Domains: cfg.Domains[svcName]},
			HealthPath: healthPath,
		}); err != nil {
			return err
		}
	}
	if live != nil {
		for svcName, domains := range live.Domains {
			if _, ok := cfg.Domains[svcName]; !ok {
				_ = app.IngressDelete(ctx, app.IngressDeleteRequest{
					Cluster: dc.Cluster,
					Route:   app.IngressRouteArg{Service: svcName, Domains: domains},
				})
			}
		}
	}
	return nil
}
