package reconcile

import (
	"context"
	"errors"
	"fmt"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// Ingress reconciles the in-cluster Caddy that fronts every domain-bearing
// service. Three steps, all idempotent:
//
//  1. EnsureCaddy: PVC + ConfigMap + Service + Deployment in kube-system.
//     Reapplied every deploy → zero drift.
//
//  2. BuildCaddyConfig from cfg.Domains plus the resolved Service ports,
//     then ReloadCaddyConfig — POST the JSON to Caddy's admin API on
//     localhost:2019. Caddy validates first; on success the listeners
//     atomically swap to the new routes without dropping connections.
//     Removed domains simply aren't in the new config and stop being served.
//     Cert files on /data are harmless residue.
//
//  3. For each domain: wait for the cert to appear on /data, then verify
//     HTTPS responds. Both probes run via Exec inside the Caddy pod — no
//     dependency on the operator's local DNS. Timeouts warn rather than
//     fail (next deploy re-verifies; Caddy keeps retrying ACME regardless).
//
// No k8s Ingress resources are created or torn down — Caddy reads its config
// from one place (the admin API), not from the cluster's Ingress objects.
func Ingress(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig) error {
	out := dc.Cluster.Log()

	kc := dc.Cluster.MasterKube
	if kc == nil {
		return errors.New("no master kube client for caddy reconcile")
	}

	names, err := dc.Cluster.Names()
	if err != nil {
		return err
	}
	ns := names.KubeNamespace()

	// 1. Caddy must exist and be Ready before we can talk to its admin API.
	out.Progress("ensuring caddy")
	if err := kc.EnsureCaddy(ctx); err != nil {
		return fmt.Errorf("ensure caddy: %w", err)
	}

	// 2. Resolve every domain-bearing service's port + build the config.
	routes := make([]kube.CaddyRoute, 0, len(cfg.Domains))
	for _, svcName := range utils.SortedKeys(cfg.Domains) {
		domains := cfg.Domains[svcName]
		if len(domains) == 0 {
			continue
		}
		port, err := kc.GetServicePort(ctx, ns, svcName)
		if err != nil {
			return fmt.Errorf("ingress: service %q has no port — needs --port: %w", svcName, err)
		}
		routes = append(routes, kube.CaddyRoute{
			Service: svcName,
			Port:    port,
			Domains: domains,
		})
	}

	configJSON, err := kube.BuildCaddyConfig(kube.CaddyConfigInput{
		Namespace: ns,
		Routes:    routes,
		ACMEEmail: cfg.ACMEEmail,
	})
	if err != nil {
		return fmt.Errorf("build caddy config: %w", err)
	}

	out.Progress("reloading caddy config")
	if err := kc.ReloadCaddyConfig(ctx, configJSON); err != nil {
		return err
	}
	out.Success("caddy config loaded")

	// 3. Per-domain cert + HTTPS verification. Warn-and-continue posture:
	// timeouts surface but don't fail the deploy. Caddy keeps trying.
	for _, svcName := range utils.SortedKeys(cfg.Domains) {
		healthPath := "/"
		if svc, ok := cfg.Services[svcName]; ok && svc.Health != "" {
			healthPath = svc.Health
		}
		for _, domain := range cfg.Domains[svcName] {
			out.Progress(fmt.Sprintf("waiting for certificate %s", domain))
			if err := kc.WaitForCaddyCert(ctx, domain); err != nil {
				out.Warning(fmt.Sprintf("%s: certificate not issued in time — next deploy will re-verify (%v)", domain, err))
				continue
			}
			out.Success(fmt.Sprintf("certificate %s ready", domain))

			url := fmt.Sprintf("https://%s%s", domain, healthPath)
			out.Progress(fmt.Sprintf("waiting for %s", url))
			if err := kc.WaitForCaddyHTTPS(ctx, domain, healthPath); err != nil {
				out.Warning(fmt.Sprintf("%s: HTTPS probe failed — next deploy will re-verify (%v)", url, err))
				continue
			}
			out.Success(fmt.Sprintf("%s live", url))
		}
	}

	return nil
}
