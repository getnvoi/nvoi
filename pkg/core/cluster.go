package core

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/getnvoi/nvoi/pkg/infra"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// ErrNoMaster is returned by CLI dispatch helpers (Service/CronDelete)
// when the cluster has been torn down — there's no master to reach for
// kube tunnel. Callers (and the renderer in internal/render/delete.go)
// treat this as idempotent success: "cluster gone, nothing to delete."
var ErrNoMaster = errors.New("no master server found")

// borrowedSSH wraps a shared connection with a no-op Close.
// Callers defer ssh.Close() — when the connection is shared (NodeShell),
// those are harmless no-ops. The owner closes the real connection.
type borrowedSSH struct {
	utils.SSHClient
}

func (borrowedSSH) Close() error { return nil }

// ProviderRef pairs a provider name with its resolved credentials.
// Used for secondary providers (DNS, storage) on request types.
type ProviderRef struct {
	Name  string
	Creds map[string]string
}

// Cluster identifies a deployment target: app + env + compute provider + SSH key.
// Embedded by every request type.
//
// Two SSH modes:
//   - NodeShell set: reconcile path. Connection established once via the
//     InfraProvider's NodeShell call after Bootstrap, shared across all
//     subsequent operations. SSH() returns a borrowed reference.
//   - NodeShell nil: on-demand path (API dispatch). SSH() connects fresh
//     each call, caller owns the connection and must close it.
//
// (Pre-#47 this field was named MasterSSH — renamed when the
// InfraProvider contract introduced providers without a host shell.)
type Cluster struct {
	AppName     string
	Env         string
	Provider    string
	Credentials map[string]string
	SSHKey      []byte
	Output      Output

	// NodeShell is the pre-established SSH connection to the host node
	// (master for IaaS, sandbox container for sandbox providers, nil for
	// providers without a host shell like managed k8s). Set by the
	// reconciler from infra.NodeShell after Bootstrap. When non-nil,
	// SSH() returns a borrowed reference (no-op Close). When nil, SSH()
	// connects on demand or fails if the provider has no node shell.
	NodeShell utils.SSHClient

	// MasterKube is the pre-established Kubernetes client returned by
	// infra.Bootstrap. Mirrors NodeShell: when set, Kube() returns a
	// borrowed reference; when nil, Kube() builds a fresh client on
	// demand and the caller owns Close().
	MasterKube *kube.Client

	// DeployHash is a per-deploy tag fragment. Set once at the top of
	// reconcile.Deploy (format YYYYMMDD-HHMMSS UTC) and inherited by
	// every downstream operation. Two jobs:
	//
	//   - Built images get tagged with it (user-tag-"-"-hash or just
	//     hash if no user tag), so every deploy produces a unique image
	//     reference and PodSpec diffs trigger an automatic rolling update.
	//   - Every nvoi-managed workload's metadata + pod template gets
	//     labelled nvoi/deploy-hash=<hash>, so `kubectl get deploy -L
	//     nvoi/deploy-hash` shows which deploy placed each resource.
	//
	// Empty string on CLI dispatch paths (nvoi logs, exec, etc.) that
	// don't run a deploy — callers there check len() before consuming.
	DeployHash string

	// SSHFunc overrides the default SSH connection for testing.
	// When nil, uses infra.ConnectSSH (production path).
	SSHFunc func(ctx context.Context, addr string) (utils.SSHClient, error)
}

// Log returns the Output, falling back to a no-op if nil.
func (c *Cluster) Log() Output {
	if c.Output != nil {
		return c.Output
	}
	return nopOutput{}
}

// nopOutput silently discards all events.
type nopOutput struct{}

func (nopOutput) Command(string, string, string, ...any) {}
func (nopOutput) Progress(string)                        {}
func (nopOutput) Success(string)                         {}
func (nopOutput) Warning(string)                         {}
func (nopOutput) Info(string)                            {}
func (nopOutput) Error(error)                            {}
func (nopOutput) Writer() io.Writer                      { return io.Discard }

// Names resolves the naming convention for this cluster.
func (c *Cluster) Names() (*utils.Names, error) {
	return utils.NewNames(c.AppName, c.Env)
}

// SSH returns an SSH client to the host node.
//
// When NodeShell is set (reconcile path): returns a borrowed reference
// (no-op Close). The reconciler owns the connection.
//
// When NodeShell is nil (CLI dispatch): resolves the InfraProvider and
// calls NodeShell — the single dialer for every backend. Returns
// `(nil, _, err)` with an actionable message when the provider has no
// node shell (managed-k8s / sandbox runtimes). Caller owns Close().
//
// cfg is the provider-facing view of the YAML; CLI passes
// `config.NewView(rt.cfg)`, reconcile passes the same. Required because
// `infra.NodeShell` may need it (e.g. label-filtered FindMaster).
func (c *Cluster) SSH(ctx context.Context, cfg provider.ProviderConfigView) (utils.SSHClient, *utils.Names, error) {
	names, err := c.Names()
	if err != nil {
		return nil, nil, err
	}
	if c.NodeShell != nil {
		return borrowedSSH{c.NodeShell}, names, nil
	}
	bctx := c.bootstrapContext(cfg)
	infraProv, err := provider.ResolveInfra(c.Provider, c.Credentials)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve infra provider: %w", err)
	}
	shell, err := infraProv.NodeShell(ctx, bctx)
	if err != nil {
		return nil, nil, fmt.Errorf("infra.NodeShell: %w", err)
	}
	if shell == nil {
		return nil, nil, fmt.Errorf("infra provider %q has no node shell — sandbox / managed-k8s providers don't expose host SSH", c.Provider)
	}
	return shell, names, nil
}

// Connect opens an SSH connection using SSHFunc or the default infra.ConnectSSH.
// Caller owns the connection and must close it.
func (c *Cluster) Connect(ctx context.Context, addr string) (utils.SSHClient, error) {
	connectFn := c.SSHFunc
	if connectFn == nil {
		connectFn = func(ctx context.Context, addr string) (utils.SSHClient, error) {
			return infra.ConnectSSH(ctx, addr, utils.DefaultUser, c.SSHKey)
		}
	}
	ssh, err := connectFn(ctx, addr)
	if err != nil {
		return nil, fmt.Errorf("ssh dial %s: %w", addr, err)
	}
	return ssh, nil
}

// Kube returns a Kubernetes client to the host node alongside a cleanup
// func the caller must defer.
//
// When MasterKube is set (reconcile path): returns a borrowed reference
// and a no-op cleanup. The reconciler owns Close().
//
// When MasterKube is nil (CLI dispatch): resolves the InfraProvider and
// calls Bootstrap — the single dialer. Bootstrap is idempotent on an
// existing cluster (≤500ms: lookup + SSH dial + kube tunnel build). The
// returned cleanup closes the kube tunnel; the cached SSH on the
// provider is released by infra.Close() at end of command.
//
// cfg is the provider-facing view of the YAML; CLI passes
// `config.NewView(rt.cfg)`, reconcile passes the same.
func (c *Cluster) Kube(ctx context.Context, cfg provider.ProviderConfigView) (*kube.Client, *utils.Names, func(), error) {
	names, err := c.Names()
	if err != nil {
		return nil, nil, nil, err
	}
	if c.MasterKube != nil {
		return c.MasterKube, names, func() {}, nil
	}
	bctx := c.bootstrapContext(cfg)
	infraProv, err := provider.ResolveInfra(c.Provider, c.Credentials)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("resolve infra provider: %w", err)
	}
	kc, err := infraProv.Bootstrap(ctx, bctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("infra.Bootstrap: %w", err)
	}
	cleanup := func() {
		_ = kc.Close()
		_ = infraProv.Close()
	}
	return kc, names, cleanup, nil
}

// bootstrapContext builds the BootstrapContext provider methods need
// from the Cluster + view. Forwards SSHFunc as SSHDial so test mocks
// intercept (mirror of internal/config.BootstrapContext).
func (c *Cluster) bootstrapContext(cfg provider.ProviderConfigView) *provider.BootstrapContext {
	bctx := &provider.BootstrapContext{
		App:          c.AppName,
		Env:          c.Env,
		ProviderName: c.Provider,
		Credentials:  c.Credentials,
		SSHKey:       c.SSHKey,
		DeployHash:   c.DeployHash,
		Output:       c.Log(),
		Cfg:          cfg,
		MasterKube:   c.MasterKube,
	}
	if c.SSHFunc != nil {
		ssh := c.SSHFunc
		bctx.SSHDial = func(ctx context.Context, addr string) (utils.SSHClient, error) {
			return ssh(ctx, addr)
		}
	}
	return bctx
}
