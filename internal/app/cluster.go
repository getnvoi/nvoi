package app

import (
	"context"
	"fmt"
	"io"

	"github.com/getnvoi/nvoi/internal/core"
	"github.com/getnvoi/nvoi/internal/infra"
	"github.com/getnvoi/nvoi/internal/provider"
)

// Cluster identifies a deployment target: app + env + compute provider + SSH key.
// Embedded by every request type. Provides methods to resolve names, provider, master, SSH.
type Cluster struct {
	AppName     string
	Env         string
	Provider    string
	Credentials map[string]string
	SSHKey      []byte
	Output      Output
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
func (c *Cluster) Names() (*core.Names, error) {
	return core.NewNames(c.AppName, c.Env)
}

// Compute resolves the compute provider.
func (c *Cluster) Compute() (provider.ComputeProvider, error) {
	return provider.ResolveCompute(c.Provider, c.Credentials)
}

// Master finds the master server via provider API.
func (c *Cluster) Master(ctx context.Context) (*provider.Server, *core.Names, provider.ComputeProvider, error) {
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
func (c *Cluster) SSH(ctx context.Context) (core.SSHClient, *core.Names, error) {
	master, names, _, err := c.Master(ctx)
	if err != nil {
		return nil, nil, err
	}
	ssh, err := infra.ConnectSSH(ctx, master.IPv4+":22", core.DefaultUser, c.SSHKey)
	if err != nil {
		return nil, nil, fmt.Errorf("ssh master: %w", err)
	}
	return ssh, names, nil
}
