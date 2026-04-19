package reconcile

import (
	"context"
	"errors"
	"fmt"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
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

	// 0. Migration cleanup: purge any orphaned tunnel agent workloads that may
	//    have been left behind from a previous providers.tunnel deploy. Runs
	//    before EnsureCaddy so there is no window where both ingress paths
	//    are active. Warn-and-continue — on a fresh cluster nothing exists.
	if err := kc.PurgeTunnelAgents(ctx, ns); err != nil {
		out.Warning(fmt.Sprintf("purge orphan tunnel agents: %v", err))
	}

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

// TunnelIngress reconciles tunnel-based ingress. Called when
// cfg.Providers.Tunnel is set — replaces Caddy entirely for this deploy.
//
//  1. Resolve the TunnelProvider from dc.Tunnel credentials.
//  2. Build routes from cfg.Domains (hostname → service:port, cluster-wide).
//  3. Call tunnel.Reconcile() → TunnelPlan.
//  4. Apply workloads (agent Deployment + Secret ± ConfigMap) to the cluster.
//  5. Write DNS CNAME records via the configured DNSProvider.
func TunnelIngress(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig) error {
	out := dc.Cluster.Log()

	tun, err := provider.ResolveTunnel(dc.Tunnel.Name, dc.Tunnel.Creds)
	if err != nil {
		return fmt.Errorf("resolve tunnel provider: %w", err)
	}

	names, err := dc.Cluster.Names()
	if err != nil {
		return err
	}
	ns := names.KubeNamespace()

	// Build the cluster-wide route table.
	routes := make([]provider.TunnelRoute, 0)
	for svcName, domains := range cfg.Domains {
		svc, ok := cfg.Services[svcName]
		if !ok {
			continue
		}
		for _, hostname := range domains {
			routes = append(routes, provider.TunnelRoute{
				Hostname:    hostname,
				ServiceName: svcName,
				ServicePort: svc.Port,
				Scheme:      "http",
			})
		}
	}

	req := provider.TunnelRequest{
		Name:      names.Base(),
		Namespace: ns,
		Labels:    names.Labels(),
		Routes:    routes,
	}

	out.Progress("reconciling tunnel")
	plan, err := tun.Reconcile(ctx, req)
	if err != nil {
		return fmt.Errorf("tunnel reconcile: %w", err)
	}

	kc := dc.Cluster.MasterKube
	if kc == nil {
		return errors.New("no master kube client for tunnel ingress")
	}

	// Apply agent workloads into the app namespace.
	for _, obj := range plan.Workloads {
		if err := kc.Apply(ctx, ns, obj); err != nil {
			return fmt.Errorf("apply tunnel workload: %w", err)
		}
	}
	out.Success("tunnel agent applied")

	// Migration cleanup: purge any orphaned Caddy workloads that may have
	// been left behind from a previous non-tunnel deploy. Caddy holds
	// hostPort 80/443 on the master — removing it ensures no resource is
	// silently consuming memory after the operator switched to a tunnel.
	// Warn-and-continue — on a fresh cluster nothing exists.
	if err := kc.PurgeCaddy(ctx); err != nil {
		out.Warning(fmt.Sprintf("purge orphan caddy: %v", err))
	}

	// Write DNS CNAME records.
	if len(plan.DNSBindings) > 0 && dc.DNS.Name != "" {
		dns, err := provider.ResolveDNS(dc.DNS.Name, dc.DNS.Creds)
		if err != nil {
			return fmt.Errorf("resolve dns provider: %w", err)
		}
		for _, hostname := range utils.SortedKeys(plan.DNSBindings) {
			binding := plan.DNSBindings[hostname]
			out.Progress(fmt.Sprintf("ensuring %s → %s", hostname, binding.DNSTarget))
			if err := dns.RouteTo(ctx, hostname, binding); err != nil {
				return fmt.Errorf("dns route %s: %w", hostname, err)
			}
			out.Success(hostname)
		}
	}

	return nil
}
