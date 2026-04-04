package core

import (
	"context"
	"fmt"
	"io"

	"github.com/getnvoi/nvoi/pkg/utils"
	"github.com/getnvoi/nvoi/pkg/infra"
	"github.com/getnvoi/nvoi/pkg/provider"
)

// ProviderRef pairs a provider name with its resolved credentials.
// Used for secondary providers (DNS, storage) on request types.
type ProviderRef struct {
	Name  string
	Creds map[string]string
}

// Cluster identifies a deployment target: app + env + compute provider + SSH key.
// Embedded by every request type. Provides methods to resolve names, provider, master, SSH.
type Cluster struct {
	AppName     string
	Env         string
	Provider    string
	Credentials map[string]string
	SSHKey      []byte
	Output      Output
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

// SSH connects to the master node and returns an SSH client + names.
// Caller must defer ssh.Close().
func (c *Cluster) SSH(ctx context.Context) (utils.SSHClient, *utils.Names, error) {
	master, names, _, err := c.Master(ctx)
	if err != nil {
		return nil, nil, err
	}
	addr := master.IPv4 + ":22"
	connectFn := c.SSHFunc
	if connectFn == nil {
		connectFn = func(ctx context.Context, addr string) (utils.SSHClient, error) {
			return infra.ConnectSSH(ctx, addr, utils.DefaultUser, c.SSHKey)
		}
	}
	ssh, err := connectFn(ctx, addr)
	if err != nil {
		return nil, nil, fmt.Errorf("ssh master: %w", err)
	}
	return ssh, names, nil
}
