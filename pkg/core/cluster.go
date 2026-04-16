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
	// Used for non-kubectl SSH: volume mount, docker, k3s bootstrap.
	MasterSSH utils.SSHClient

	// Kube is the client-go k8s API client. When set, all kubectl operations
	// go through it — no shell commands, no kubectl binary.
	// Agent: direct to localhost:6443. CLI bootstrap: SSH-tunneled.
	Kube *kube.KubeClient

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
