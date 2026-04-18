package aws

import (
	"context"
	"fmt"
	"sync"

	"github.com/getnvoi/nvoi/pkg/infra"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// InfraProvider satisfaction. Same staging pattern as Hetzner — small
// methods ship working today (IngressBinding / HasPublicIngress /
// ConsumesBlocks / ValidateConfig / NodeShell / Close /
// ValidateCredentials / ListResources via the existing concrete client
// surface). The orchestration-heavy methods (Bootstrap / LiveSnapshot /
// TeardownOrphans / Teardown) ship as stubs that the next refactor
// commits fill in alongside the reconcile rewrite.
//
// Lifecycle: shell caches the SSH connection across Bootstrap → NodeShell
// → end-of-deploy. Close() releases it. NodeShell on the dispatch path
// (no preceding Bootstrap in this process) cold-dials.

var awsCacheMu sync.Mutex

func (c *Client) cachedShell() utils.SSHClient {
	awsCacheMu.Lock()
	defer awsCacheMu.Unlock()
	return c.shell
}

func (c *Client) setCachedShell(s utils.SSHClient) {
	awsCacheMu.Lock()
	defer awsCacheMu.Unlock()
	c.shell = s
}

// Bootstrap converges AWS infra to a working k3s cluster.
//
// STAGE 1 of refactor #47: returns ErrNotImplemented. Reconcile drives
// orchestration through the legacy pkg/core wrappers; this stub declares
// interface satisfaction so RegisterInfra works. Stage 2 inlines the
// orchestration here and switches reconcile.Deploy over.
func (c *Client) Bootstrap(ctx context.Context, dc *provider.BootstrapContext) (*kube.Client, error) {
	return nil, fmt.Errorf("aws.Bootstrap: %w", provider.ErrNotImplemented)
}

func (c *Client) LiveSnapshot(ctx context.Context, dc *provider.BootstrapContext) (*provider.LiveSnapshot, error) {
	return nil, fmt.Errorf("aws.LiveSnapshot: %w", provider.ErrNotImplemented)
}

func (c *Client) TeardownOrphans(ctx context.Context, dc *provider.BootstrapContext, live *provider.LiveSnapshot) error {
	return fmt.Errorf("aws.TeardownOrphans: %w", provider.ErrNotImplemented)
}

func (c *Client) Teardown(ctx context.Context, dc *provider.BootstrapContext, deleteVolumes bool) error {
	return fmt.Errorf("aws.Teardown: %w", provider.ErrNotImplemented)
}

// IngressBinding returns the master's public IPv4 wrapped in a DNS-A hint.
func (c *Client) IngressBinding(ctx context.Context, dc *provider.BootstrapContext, _ provider.ServiceTarget) (provider.IngressBinding, error) {
	master, err := c.findMaster(ctx, dc)
	if err != nil {
		return provider.IngressBinding{}, err
	}
	return provider.IngressBinding{DNSType: "A", DNSTarget: master.IPv4}, nil
}

// HasPublicIngress: every AWS instance nvoi provisions has a public IPv4
// (auto-assigned to subnet on the public route table). Caddy hostPort
// binds on the master.
func (c *Client) HasPublicIngress() bool { return true }

// ConsumesBlocks: AWS reads the same IaaS YAML blocks Hetzner does.
func (c *Client) ConsumesBlocks() []string {
	return []string{"servers", "firewall", "volumes"}
}

// ValidateConfig: AWS-specific invariants. Today this is a no-op — the
// generic validator covers what matters (master count, role values, etc.).
// AWS root disk IS configurable (unlike Hetzner) so `disk:` is allowed.
func (c *Client) ValidateConfig(cfg provider.ProviderConfigView) error {
	return nil
}

// NodeShell: cached connection from Bootstrap, or fresh dial via the
// public IPv4 looked up by label. Caller manages Close via Cluster.
func (c *Client) NodeShell(ctx context.Context, dc *provider.BootstrapContext) (utils.SSHClient, error) {
	if s := c.cachedShell(); s != nil {
		return s, nil
	}
	master, err := c.findMaster(ctx, dc)
	if err != nil {
		return nil, fmt.Errorf("aws.NodeShell: %w", err)
	}
	conn, err := infra.ConnectSSH(ctx, master.IPv4+":22", utils.DefaultUser, dc.SSHKey)
	if err != nil {
		return nil, fmt.Errorf("aws.NodeShell dial %s: %w", master.IPv4, err)
	}
	c.setCachedShell(conn)
	return conn, nil
}

// Close releases the cached SSH if Bootstrap (or NodeShell's cold path)
// established one. Idempotent.
func (c *Client) Close() error {
	awsCacheMu.Lock()
	s := c.shell
	c.shell = nil
	awsCacheMu.Unlock()
	if s == nil {
		return nil
	}
	return s.Close()
}

// findMaster locates the master EC2 instance by tag. Replaces pkg/core's
// FindMaster (which depends on the doomed ComputeProvider interface).
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
		master.PrivateIP = ip
	}
	return master, nil
}

// Compile-time check that *Client satisfies provider.InfraProvider.
var _ provider.InfraProvider = (*Client)(nil)

// kube import retained for Bootstrap's Stage 2 fill-in.
var _ = (*kube.Client)(nil)
