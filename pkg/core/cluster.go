package core

import (
	"context"
	"fmt"
	"io"

	"github.com/getnvoi/nvoi/pkg/infra"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// borrowedSSH wraps a shared connection with a no-op Close.
// Callers defer ssh.Close() — when the connection is shared (MasterSSH),
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
//   - MasterSSH set: reconcile path. Connection established once after Servers(),
//     shared across all subsequent operations. SSH() returns a borrowed reference.
//   - MasterSSH nil: on-demand path (API dispatch). SSH() connects fresh each call,
//     caller owns the connection and must close it.
type Cluster struct {
	AppName     string
	Env         string
	Provider    string
	Credentials map[string]string
	SSHKey      []byte
	Output      Output

	// MasterSSH is the pre-established SSH connection to the master node.
	// Set once after Servers() in reconcile. When set, SSH() returns a
	// borrowed reference (no-op Close). When nil, SSH() connects on demand.
	MasterSSH utils.SSHClient

	// MasterKube is the pre-established Kubernetes client over the master
	// SSH tunnel. Mirrors MasterSSH: set once after MasterSSH in reconcile;
	// Kube() returns a borrowed reference. When nil, Kube() builds a fresh
	// client on demand and the caller owns Close().
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

// Compute resolves the compute provider.
func (c *Cluster) Compute() (provider.ComputeProvider, error) {
	return provider.ResolveCompute(c.Provider, c.Credentials)
}

// Master finds the master server via provider API.
func (c *Cluster) Master(ctx context.Context) (*provider.Server, *utils.Names, provider.ComputeProvider, error) {
	names, err := c.Names()
	if err != nil {
		return nil, nil, nil, err
	}
	prov, err := c.Compute()
	if err != nil {
		return nil, nil, nil, err
	}
	master, err := FindMaster(ctx, prov, names)
	if err != nil {
		return nil, nil, nil, err
	}
	return master, names, prov, nil
}

// SSH returns an SSH client to the master node.
//
// When MasterSSH is set (reconcile path): returns a borrowed reference with
// no-op Close. The connection is owned by the reconciler.
//
// When MasterSSH is nil (API dispatch): connects fresh via connect().
// Caller owns the connection and must close it.
func (c *Cluster) SSH(ctx context.Context) (utils.SSHClient, *utils.Names, error) {
	if c.MasterSSH != nil {
		names, err := c.Names()
		if err != nil {
			return nil, nil, err
		}
		return borrowedSSH{c.MasterSSH}, names, nil
	}

	// On-demand: find master, connect, caller owns connection.
	master, names, _, err := c.Master(ctx)
	if err != nil {
		return nil, nil, err
	}
	conn, err := c.Connect(ctx, master.IPv4+":22")
	if err != nil {
		return nil, nil, err
	}
	return conn, names, nil
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
