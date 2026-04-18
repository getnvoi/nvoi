package hetzner

import (
	"context"
	"fmt"
	"sync"

	"github.com/getnvoi/nvoi/pkg/infra"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// InfraProvider satisfaction. The orchestration-heavy methods (Bootstrap /
// LiveSnapshot / TeardownOrphans / Teardown) are stubbed during this commit
// — the next commits in the refactor relocate the convergence logic from
// internal/reconcile/{servers,firewall,volumes}.go and pkg/core/{compute,
// firewall,volume}.go into private helpers in this package, then wire the
// stubs to call them. The small methods (IngressBinding, HasPublicIngress,
// ConsumesBlocks, ValidateConfig, NodeShell, Close, ListResources,
// ValidateCredentials) ship working today — they delegate to the existing
// concrete *Client surface (ListServers / GetPrivateIP / etc.).
//
// Lifecycle: cachedShell holds the SSH connection Bootstrap dialed for the
// kube tunnel. NodeShell returns it (avoids a duplicate dial). Close()
// tears it down. On the dispatch path (NodeShell called without a
// preceding Bootstrap), the cache is nil and NodeShell dials fresh.

// cachedShell stays nil until Bootstrap dials — then NodeShell returns the
// same connection. shellMu serializes lazy NodeShell dials on the dispatch
// path so two `nvoi ssh` invocations on the same provider receiver don't
// race the cache.
var (
	hetznerCacheMu sync.Mutex
)

func (c *Client) cachedShell() utils.SSHClient {
	hetznerCacheMu.Lock()
	defer hetznerCacheMu.Unlock()
	return c.shell
}

func (c *Client) setCachedShell(s utils.SSHClient) {
	hetznerCacheMu.Lock()
	defer hetznerCacheMu.Unlock()
	c.shell = s
}

// Bootstrap converges Hetzner infra to a working k3s cluster.
//
// STAGE 1 of refactor #47: returns ErrNotImplemented. The reconciler still
// drives orchestration through the legacy pkg/core wrappers; this stub
// declares interface satisfaction so RegisterInfra works. Stage 2 (commit
// 6) inlines the orchestration here and rewires reconcile.Deploy to call
// this method instead of the legacy steps.
func (c *Client) Bootstrap(ctx context.Context, dc *provider.BootstrapContext) (*kube.Client, error) {
	return nil, fmt.Errorf("hetzner.Bootstrap: %w", provider.ErrNotImplemented)
}

// LiveSnapshot returns the provider-side view of live infra (servers,
// volumes, firewalls) for orphan-detection input.
//
// STAGE 1 of refactor #47: stub. Stage 2 wires this to call the underlying
// list methods filtered by cluster labels. The implementation is trivially
// derivable from the existing ListServers / ListVolumes / ListAllFirewalls
// methods on *Client and lands together with reconcile's switchover.
func (c *Client) LiveSnapshot(ctx context.Context, dc *provider.BootstrapContext) (*provider.LiveSnapshot, error) {
	return nil, fmt.Errorf("hetzner.LiveSnapshot: %w", provider.ErrNotImplemented)
}

// TeardownOrphans removes infra no longer referenced by the desired state.
//
// STAGE 1: stub. Stage 2 inlines the contents of the legacy
// reconcile.{ServersRemoveOrphans,FirewallRemoveOrphans,Volumes-orphans}
// and gates DeleteVolume on whether the volume is still referenced by cfg.
func (c *Client) TeardownOrphans(ctx context.Context, dc *provider.BootstrapContext, live *provider.LiveSnapshot) error {
	return fmt.Errorf("hetzner.TeardownOrphans: %w", provider.ErrNotImplemented)
}

// Teardown nukes every Hetzner resource matching this cluster's labels.
// Backs `bin/destroy`. With deleteVolumes=false, persistent volumes are
// detached but preserved.
//
// STAGE 1: stub. Stage 2 inlines internal/core/teardown.go's compute /
// firewall / network / (optional) volume nuke loops.
func (c *Client) Teardown(ctx context.Context, dc *provider.BootstrapContext, deleteVolumes bool) error {
	return fmt.Errorf("hetzner.Teardown: %w", provider.ErrNotImplemented)
}

// IngressBinding returns the master's public IPv4 wrapped in a DNS-A hint.
// Cloudflare may proxy and rewrite to its own representation; non-proxying
// providers write a plain A record.
func (c *Client) IngressBinding(ctx context.Context, dc *provider.BootstrapContext, _ provider.ServiceTarget) (provider.IngressBinding, error) {
	master, err := c.findMaster(ctx, dc)
	if err != nil {
		return provider.IngressBinding{}, err
	}
	return provider.IngressBinding{DNSType: "A", DNSTarget: master.IPv4}, nil
}

// HasPublicIngress: every Hetzner server has a routable public IPv4. Caddy
// can bind hostPort:80/443 on the master node for ingress termination.
func (c *Client) HasPublicIngress() bool { return true }

// ConsumesBlocks: Hetzner reads the YAML blocks the existing IaaS reconcile
// already consumes. Validator rejects any other top-level provider block
// (e.g. `sandbox:` under hetzner) with an actionable error.
func (c *Client) ConsumesBlocks() []string {
	return []string{"servers", "firewall", "volumes"}
}

// ValidateConfig enforces Hetzner-specific invariants beyond the generic
// validator. The single hard rule today: `disk:` is illegal because
// Hetzner root disk is fixed per server type — there is no API to set or
// resize it. The generic validator already rejects it for hetzner; we
// repeat the check defensively here so the contract lives next to the
// implementation, not three layers up.
func (c *Client) ValidateConfig(cfg provider.ProviderConfigView) error {
	for _, s := range cfg.ServerDefs() {
		if s.Disk > 0 {
			return fmt.Errorf("servers.%s.disk: hetzner root disk is fixed per server type — remove disk: from config", s.Name)
		}
	}
	return nil
}

// NodeShell returns an SSH client to the master. Two paths:
//
//  1. Deploy path: Bootstrap already dialed and cached. Reuse — avoids a
//     second connection to the same host.
//  2. Dispatch path (`nvoi ssh` without a prior deploy in this process):
//     find master by label, dial fresh. Caller owns Close via Cluster.
//
// Returns (nil, error) on hard failures. Returns (nil, nil) is reserved
// for providers that genuinely don't expose host shell — Hetzner always
// does, so we never return (nil, nil).
func (c *Client) NodeShell(ctx context.Context, dc *provider.BootstrapContext) (utils.SSHClient, error) {
	if s := c.cachedShell(); s != nil {
		return s, nil
	}
	master, err := c.findMaster(ctx, dc)
	if err != nil {
		return nil, fmt.Errorf("hetzner.NodeShell: %w", err)
	}
	conn, err := infra.ConnectSSH(ctx, master.IPv4+":22", utils.DefaultUser, dc.SSHKey)
	if err != nil {
		return nil, fmt.Errorf("hetzner.NodeShell dial %s: %w", master.IPv4, err)
	}
	c.setCachedShell(conn)
	return conn, nil
}

// Close releases the cached SSH if Bootstrap (or NodeShell's cold path)
// established one. Idempotent.
func (c *Client) Close() error {
	hetznerCacheMu.Lock()
	s := c.shell
	c.shell = nil
	hetznerCacheMu.Unlock()
	if s == nil {
		return nil
	}
	return s.Close()
}

// findMaster locates the master server by cluster labels using the
// existing ListServers + GetPrivateIP surface. Replaces pkg/core's
// FindMaster, which depended on the doomed ComputeProvider interface.
// Private to the package — IngressBinding / NodeShell are the only
// callers.
func (c *Client) findMaster(ctx context.Context, dc *provider.BootstrapContext) (*provider.Server, error) {
	names, err := utils.NewNames(dc.App, dc.Env)
	if err != nil {
		return nil, err
	}
	labels := names.Labels()
	labels["role"] = "master"
	masters, err := c.ListServers(ctx, labels)
	if err != nil {
		return nil, fmt.Errorf("find master: %w", err)
	}
	if len(masters) == 0 {
		return nil, fmt.Errorf("no master server found for %s/%s", dc.App, dc.Env)
	}
	master := masters[0]
	if master.PrivateIP == "" {
		ip, err := c.GetPrivateIP(ctx, master.ID)
		if err != nil {
			return nil, fmt.Errorf("resolve master private IP: %w", err)
		}
		if ip == "" {
			return nil, fmt.Errorf("master %s has no private IP — private network may not be attached", master.Name)
		}
		master.PrivateIP = ip
	}
	return master, nil
}

// Compile-time check that *Client satisfies provider.InfraProvider.
var _ provider.InfraProvider = (*Client)(nil)

// kube import retained — Bootstrap (filled in by C6) returns *kube.Client.
// Reference here keeps the import valid through the staged rollout without
// turning the import into a runtime no-op once Bootstrap inlines.
var _ = (*kube.Client)(nil)
