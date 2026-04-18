package core

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/getnvoi/nvoi/pkg/infra"
	"github.com/getnvoi/nvoi/pkg/kube"
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

// SSH returns the borrowed SSH client to the host node. Caller owns the
// reference but must NOT Close it (no-op). Returns an error if NodeShell
// is nil — the CLI dispatch path is responsible for resolving the
// InfraProvider and populating NodeShell BEFORE calling this (see
// cmd/cli/ssh.go's nil-check). Reconcile populates NodeShell during
// Deploy. No on-demand fallback any more — the InfraProvider's
// NodeShell method is the single dialer.
func (c *Cluster) SSH(ctx context.Context) (utils.SSHClient, *utils.Names, error) {
	if c.NodeShell == nil {
		return nil, nil, fmt.Errorf("no node shell available — caller must populate Cluster.NodeShell via infra.NodeShell before SSH()")
	}
	names, err := c.Names()
	if err != nil {
		return nil, nil, err
	}
	return borrowedSSH{c.NodeShell}, names, nil
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

// Kube returns a Kubernetes client to the master node alongside a cleanup
// func the caller must defer.
//
// When MasterKube is set (reconcile path): returns a borrowed reference and
// a no-op cleanup. The reconciler owns Close().
//
// When MasterKube is nil (CLI dispatch): finds the master, opens a fresh SSH
// connection + tunnel, builds a Client. cleanup() closes the kube tunnel and
// the underlying SSH connection.
func (c *Cluster) Kube(ctx context.Context) (*kube.Client, *utils.Names, func(), error) {
	if c.MasterKube != nil {
		names, err := c.Names()
		if err != nil {
			return nil, nil, nil, err
		}
		return c.MasterKube, names, func() {}, nil
	}

	ssh, names, err := c.SSH(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	kc, err := kube.New(ctx, ssh)
	if err != nil {
		ssh.Close()
		return nil, nil, nil, fmt.Errorf("kube client: %w", err)
	}
	cleanup := func() {
		_ = kc.Close()
		_ = ssh.Close()
	}
	return kc, names, cleanup, nil
}
