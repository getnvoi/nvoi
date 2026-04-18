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
// Linear flow per refactor #47 — zero per-provider branching:
//
//  1. ValidateConfig + cfg.Resolve()
//  2. Stamp DeployHash
//  3. Build local images (pre-infra; failure aborts before any provisioning)
//  4. DescribeLive (provider-side via infra.LiveSnapshot, kube-side via Describe)
//  5. infra.Bootstrap → returns *kube.Client; provider owns servers/firewall/volumes
//  6. infra.NodeShell → optional SSH client for `nvoi ssh`
//  7. EnsureNamespace + Registries + Secrets + Storage
//  8. Services + Crons
//  9. infra.TeardownOrphans (servers/firewall/volumes diff)
//  10. Ingress (gated): RouteDomains via dns.RouteTo + EnsureCaddy + cert/HTTPS waits
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

	// Validator may have migrated providers.compute → providers.infra.
	// Sync onto Cluster.Provider so legacy pkg/core helpers (DNSSet,
	// Storage, Service.Master()) that resolve the provider via Cluster
	// see the right name. Removed in C10 alongside Cluster.Compute() /
	// Cluster.Master().
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

	live, err := DescribeLive(ctx, dc, cfg)
	if err != nil {
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
	// client. Caller treats as opaque — lifetime varies from 200ms
	// (managed k8s authn) to 170s (fresh IaaS VMs + k3s install).
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
		dc.Cluster.MasterSSH = ns
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
	if err := Registries(ctx, dc, live, cfg); err != nil {
		return fmt.Errorf("registries: %w", err)
	}

	secretValues, err := Secrets(ctx, dc, live, cfg)
	if err != nil {
		return err
	}

	storageCreds, err := Storage(ctx, dc, live, cfg)
	if err != nil {
		return err
	}

	sources := mergeSources(secretValues, storageCreds)

	if err := Services(ctx, dc, live, cfg, sources); err != nil {
		return err
	}
	if err := Crons(ctx, dc, live, cfg, sources); err != nil {
		return err
	}

	// Workloads have moved. Now safe to drain + delete orphan servers,
	// firewalls, and volumes via the infra provider.
	infraLive := liveToSnapshot(live)
	if err := infra.TeardownOrphans(ctx, bctx, infraLive); err != nil {
		return err
	}

	// Ingress: gated by HasPublicIngress + len(domains). Once #49 lands
	// (tunnel providers), this branch additionally checks
	// cfg.Providers.Tunnel == "" — sandbox / managed-k8s without a public
	// IP route through the tunnel instead of Caddy.
	if infra.HasPublicIngress() && len(cfg.Domains) > 0 {
		if err := RouteDomains(ctx, dc, cfg, live, infra, bctx); err != nil {
			return err
		}
		verifyDNSPropagation(ctx, dc, cfg)
		if err := Ingress(ctx, dc, live, cfg); err != nil {
			return err
		}
	}

	return nil
}

// RouteDomains writes a DNS record per (service, domain) pair via the
// configured DNSProvider. The IngressBinding (DNS type + target) comes
// from the InfraProvider — IaaS returns A/IPv4, managed-k8s would return
// CNAME/lb-hostname, and the DNSProvider picks its native representation
// from there. Orphan-domain cleanup uses live.Domains (populated by
// DescribeLive from the kube ingress objects).
func RouteDomains(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig, live *config.LiveState, infra provider.InfraProvider, bctx *provider.BootstrapContext) error {
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
	// Orphan-domain cleanup — services dropped from cfg get their DNS
	// torn down. Best-effort; warnings on failure (matches legacy DNS()
	// step behavior).
	if live != nil {
		for svcName, domains := range live.Domains {
			if _, kept := cfg.Domains[svcName]; kept {
				continue
			}
			for _, domain := range domains {
				if err := dns.Unroute(ctx, domain); err != nil {
					dc.Cluster.Log().Warning(fmt.Sprintf("orphan dns %s not removed: %s", domain, err))
				}
			}
		}
	}
	return nil
}

// liveToSnapshot projects the reconciler-collected LiveState into the
// LiveSnapshot the InfraProvider's TeardownOrphans expects.
func liveToSnapshot(state *config.LiveState) *provider.LiveSnapshot {
	if state == nil {
		return nil
	}
	return &provider.LiveSnapshot{
		Servers:    append([]string(nil), state.Servers...),
		ServerDisk: copyIntMap(state.ServerDisk),
		Firewalls:  append([]string(nil), state.Firewalls...),
		Volumes:    append([]string(nil), state.Volumes...),
	}
}

func copyIntMap(m map[string]int) map[string]int {
	if m == nil {
		return nil
	}
	cp := make(map[string]int, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

func sortedDomainKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Avoid pulling in sort just here — len(m) is small and order matters
	// only for the equivalence test, which alphabetizes via this fn.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
}
