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
	if len(cfg.Domains) > 0 {
		// Ensure Traefik ACME is configured before applying any ingress.
		ssh := dc.Cluster.MasterSSH
		if ssh == nil {
			return fmt.Errorf("no master SSH connection for traefik ACME setup")
		}
		email := cfg.ACMEEmail
		if email == "" {
			email = "acme@" + firstConfigDomain(cfg)
		}
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
	}

	// Orphan cleanup runs regardless of whether cfg.Domains is empty.
	// A service that had domains last deploy but doesn't now still has an
	// Ingress resource in the cluster that must be removed.
	if live != nil {
		for svcName, domains := range live.Domains {
			if _, ok := cfg.Domains[svcName]; !ok {
				if err := app.IngressDelete(ctx, app.IngressDeleteRequest{
					Cluster: dc.Cluster,
					Route:   app.IngressRouteArg{Service: svcName, Domains: domains},
				}); err != nil {
					dc.Cluster.Log().Warning(fmt.Sprintf("orphan ingress for %s not removed: %s", svcName, err))
				}
			}
		}
	}
	return nil
}

// firstConfigDomain returns the first domain from the config for ACME email derivation.
// Only called when cfg.Domains is non-empty (caller checks).
func firstConfigDomain(cfg *config.AppConfig) string {
	for _, svcName := range utils.SortedKeys(cfg.Domains) {
		if domains := cfg.Domains[svcName]; len(domains) > 0 {
			return domains[0]
		}
	}
	return "" // unreachable when called after len(cfg.Domains) > 0 check
}
