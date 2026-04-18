package reconcile

import (
	"context"
	"fmt"
	"time"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/pkg/provider"
)

// Deploy reconciles live infrastructure to match the YAML config.
//
// Linear flow per refactor #47 — zero per-provider branching, no
// pre-deploy DescribeLive (each step looks up the live state it
// actually needs from kube or its own provider):
//
//  1. ValidateConfig + cfg.Resolve()
//  2. Stamp DeployHash
//  3. Build local images (pre-infra; failure aborts before any provisioning)
//  4. infra.Bootstrap → returns *kube.Client; provider owns servers/firewall/volumes
//  5. infra.NodeShell → optional SSH client for `nvoi ssh`
//  6. EnsureNamespace + Registries + Secrets + Storage
//  7. Services + Crons (each does its own kube-side orphan lookup)
//  8. infra.TeardownOrphans (provider does its own LiveSnapshot lookup)
//  9. Ingress (gated): RouteDomains via dns.RouteTo + EnsureCaddy + cert/HTTPS waits
//
// The reconciler never branches on "what kind of provider is this" — gates
// (HasPublicIngress, returned-nil NodeShell, ConsumesBlocks) carry every
// distinction.
func Deploy(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig) error {
	if err := ValidateConfig(cfg); err != nil {
		return err
	}
	if err := cfg.Resolve(); err != nil {
		return err
	}

	// Sync cfg.Providers.Infra onto Cluster.Provider so legacy pkg/core
	// helpers that resolve the provider via Cluster see the right name.
	if dc.Cluster.Provider == "" {
		dc.Cluster.Provider = cfg.Providers.Infra
	}

	// Per-deploy hash stamps built image tags AND every workload's
	// metadata + pod template. Format is a sortable UTC timestamp down
	// to the second — unique per `bin/deploy` run.
	dc.Cluster.DeployHash = time.Now().UTC().Format("20060102-150405")

	// Build services with `build:` declared BEFORE touching infra. A
	// build failure should never leave us with half-provisioned servers.
	if err := Build(ctx, dc, cfg); err != nil {
		return err
	}

	// Resolve the infra provider once; reuse for the whole deploy.
	bctx := config.BootstrapContext(dc, cfg)
	infra, err := provider.ResolveInfra(bctx.ProviderName, dc.Cluster.Credentials)
	if err != nil {
		return fmt.Errorf("resolve infra provider: %w", err)
	}
	defer func() { _ = infra.Close() }()

	// Bootstrap: provider provisions servers/firewall/volumes (or sandbox,
	// or auths against a managed control plane), returns a working kube
	// client. WRITE contract — drift reconciled, missing resources
	// created. Caller treats as opaque.
	kc, err := infra.Bootstrap(ctx, bctx)
	if err != nil {
		return fmt.Errorf("infra.Bootstrap: %w", err)
	}
	defer kc.Close()
	dc.Cluster.MasterKube = kc

	// Optional node shell for `nvoi ssh` and any infra-internal helper
	// that wants to exec on the host. Providers without host shell return
	// (nil, nil); CLI feature-gates on nil.
	if ns, err := infra.NodeShell(ctx, bctx); err != nil {
		return fmt.Errorf("infra.NodeShell: %w", err)
	} else if ns != nil {
		dc.Cluster.NodeShell = ns
	}

	// App-namespace must exist before any per-service secret / workload
	// write — otherwise the first writer races and fails with "namespaces
	// not found."
	names, err := dc.Cluster.Names()
	if err != nil {
		return err
	}
	if err := kc.EnsureNamespace(ctx, names.KubeNamespace()); err != nil {
		return fmt.Errorf("ensure namespace: %w", err)
	}

	// Registry pull credentials must land before Services/Crons —
	// kubelet reads imagePullSecrets at first image pull.
	if err := Registries(ctx, dc, cfg); err != nil {
		return fmt.Errorf("registries: %w", err)
	}

	secretValues, err := Secrets(ctx, dc, cfg)
	if err != nil {
		return err
	}

	storageCreds, err := Storage(ctx, dc, cfg)
	if err != nil {
		return err
	}

	sources := mergeSources(secretValues, storageCreds)

	if err := Services(ctx, dc, cfg, sources); err != nil {
		return err
	}
	if err := Crons(ctx, dc, cfg, sources); err != nil {
		return err
	}

	// Workloads have moved. Now safe for the provider to drain + delete
	// orphan servers / firewalls / volumes — it does its own LiveSnapshot
	// lookup internally (no live param threaded through).
	if err := infra.TeardownOrphans(ctx, bctx); err != nil {
		return err
	}

	// Ingress: gated by HasPublicIngress + len(domains). Once #49 lands
	// (tunnel providers), this branch additionally checks
	// cfg.Providers.Tunnel == "" — sandbox / managed-k8s without a public
	// IP route through the tunnel instead of Caddy.
	if infra.HasPublicIngress() && len(cfg.Domains) > 0 {
		if err := RouteDomains(ctx, dc, cfg, infra, bctx); err != nil {
			return err
		}
		verifyDNSPropagation(ctx, dc, cfg)
		if err := Ingress(ctx, dc, cfg); err != nil {
			return err
		}
	}

	return nil
}

// RouteDomains writes a DNS record per (service, domain) pair via the
// configured DNSProvider. The IngressBinding (DNS type + target) comes
// from the InfraProvider — IaaS returns A/IPv4, managed-k8s would return
// CNAME/lb-hostname, and the DNSProvider picks its native representation
// from there.
//
// Orphan-domain cleanup: queries Caddy's live config (kc.GetCaddyRoutes)
// for currently-served domains and unroutes any that aren't in cfg.
// Caddy is the source of truth for live ingress (we don't keep state
// ourselves).
func RouteDomains(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig, infra provider.InfraProvider, bctx *provider.BootstrapContext) error {
	if len(cfg.Domains) == 0 {
		return nil
	}
	dns, err := provider.ResolveDNS(dc.DNS.Name, dc.DNS.Creds)
	if err != nil {
		return fmt.Errorf("resolve dns provider: %w", err)
	}
	for _, svcName := range sortedDomainKeys(cfg.Domains) {
		binding, err := infra.IngressBinding(ctx, bctx, provider.ServiceTarget{
			Name: svcName,
			Port: cfg.Services[svcName].Port,
		})
		if err != nil {
			return fmt.Errorf("ingress binding for %s: %w", svcName, err)
		}
		dc.Cluster.Log().Command("dns", "set", svcName, "ip", binding.DNSTarget, "domains", cfg.Domains[svcName])
		for _, domain := range cfg.Domains[svcName] {
			dc.Cluster.Log().Progress(fmt.Sprintf("ensuring %s → %s", domain, binding.DNSTarget))
			if err := dns.RouteTo(ctx, domain, binding); err != nil {
				return fmt.Errorf("dns route %s: %w", domain, err)
			}
			dc.Cluster.Log().Success(domain)
		}
	}

	// Orphan-domain cleanup — query Caddy for live routes; any domain
	// not in cfg gets Unroute'd. Best-effort; Caddy may not be running
	// yet on first deploy (no orphans possible then anyway).
	desiredDomains := map[string]bool{}
	for _, svcDomains := range cfg.Domains {
		for _, d := range svcDomains {
			desiredDomains[d] = true
		}
	}
	if routes, err := dc.Cluster.MasterKube.GetCaddyRoutes(ctx); err == nil {
		for _, r := range routes {
			for _, domain := range r.Domains {
				if desiredDomains[domain] {
					continue
				}
				if err := dns.Unroute(ctx, domain); err != nil {
					dc.Cluster.Log().Warning(fmt.Sprintf("orphan dns %s not removed: %s", domain, err))
				}
			}
		}
	}
	return nil
}

func sortedDomainKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
}
