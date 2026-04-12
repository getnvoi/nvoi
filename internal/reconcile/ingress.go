package reconcile

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/internal/config"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/utils"
)

func Ingress(ctx context.Context, dc *config.DeployContext, live *config.LiveState, cfg *config.AppConfig) error {
	if len(cfg.Domains) == 0 {
		return nil
	}

	// Ensure Traefik ACME is configured before applying any ingress.
	ssh := dc.Cluster.MasterSSH
	if ssh == nil {
		return fmt.Errorf("no master SSH connection for traefik ACME setup")
	}
	firstDomain := firstConfigDomain(cfg)
	email := "acme@" + firstDomain
	if err := kube.EnsureTraefikACME(ctx, ssh, email, true); err != nil {
		return fmt.Errorf("traefik ACME: %w", err)
	}

	for _, svcName := range utils.SortedKeys(cfg.Domains) {
		var healthPath string
		if svc, ok := cfg.Services[svcName]; ok {
			healthPath = svc.Health
		}
		if err := app.IngressSet(ctx, app.IngressSetRequest{
			Cluster:    dc.Cluster,
			Route:      app.IngressRouteArg{Service: svcName, Domains: cfg.Domains[svcName]},
			HealthPath: healthPath,
			ACME:       true, // direct mode — Traefik handles TLS via Let's Encrypt
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

// firstConfigDomain returns the first domain from the config for ACME email.
func firstConfigDomain(cfg *config.AppConfig) string {
	for _, svcName := range utils.SortedKeys(cfg.Domains) {
		if domains := cfg.Domains[svcName]; len(domains) > 0 {
			return domains[0]
		}
		_ = svcName
	}
	return "example.com"
}
