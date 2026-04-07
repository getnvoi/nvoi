package core

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/getnvoi/nvoi/pkg/infra"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// sshCache holds a cached SSH connection to the master node.
// Shared across Cluster copies via pointer — Cluster is embedded by value
// in every request struct, but all copies point to the same cache.
type sshCache struct {
	mu    sync.Mutex
	conn  utils.SSHClient
	names *utils.Names
	addr  string // master addr this connection targets
}

// borrowedSSH wraps a cached connection with a no-op Close.
// Individual functions defer ssh.Close() — with borrowing, those
// are harmless no-ops. The real connection is closed via Cluster.Close().
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
// Embedded by every request type. Provides methods to resolve names, provider, master, SSH.
//
// SSH connection reuse: when ssh is non-nil, Cluster.SSH() returns the cached
// connection (wrapped in borrowedSSH so callers' defer ssh.Close() are no-ops).
// Call Cluster.Close() once at the end to release the real connection.
// The cache is shared across copies because ssh is a pointer.
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
	// ssh caches the master SSH connection across calls. Shared across
	// Cluster copies via pointer. Nil = no caching (each call connects fresh).
	ssh *sshCache
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

// EnableSSHCache activates SSH connection reuse. Subsequent calls to SSH()
// return the same connection (wrapped so callers' defer Close() are no-ops).
// Call Close() once when done to release the real connection.
// Must be called before any SSH() call — typically right after constructing the Cluster.
func (c *Cluster) EnableSSHCache() {
	c.ssh = &sshCache{}
}

// Close releases the cached SSH connection, if any.
// Safe to call multiple times or when no cache is active.
func (c *Cluster) Close() error {
	if c.ssh == nil {
		return nil
	}
	c.ssh.mu.Lock()
	defer c.ssh.mu.Unlock()
	if c.ssh.conn != nil {
		err := c.ssh.conn.Close()
		c.ssh.conn = nil
		c.ssh.names = nil
		c.ssh.addr = ""
		return err
	}
	return nil
}

// SSH connects to the master node and returns an SSH client + names.
// Caller must defer ssh.Close() — when caching is enabled, this is a no-op.
func (c *Cluster) SSH(ctx context.Context) (utils.SSHClient, *utils.Names, error) {
	master, names, _, err := c.Master(ctx)
	if err != nil {
		return nil, nil, err
	}
	addr := master.IPv4 + ":22"

	// If caching is enabled, return the cached connection (or create + cache it).
	if c.ssh != nil {
		c.ssh.mu.Lock()
		defer c.ssh.mu.Unlock()

		// If cached connection targets the same address, reuse it.
		if c.ssh.conn != nil && c.ssh.addr == addr {
			return borrowedSSH{c.ssh.conn}, c.ssh.names, nil
		}

		// Different address or first call — close stale connection and connect fresh.
		if c.ssh.conn != nil {
			c.ssh.conn.Close()
		}

		conn, err := c.connect(ctx, addr)
		if err != nil {
			return nil, nil, err
		}
		c.ssh.conn = conn
		c.ssh.names = names
		c.ssh.addr = addr
		return borrowedSSH{conn}, names, nil
	}

	// No caching — connect fresh (original behavior).
	conn, err := c.connect(ctx, addr)
	if err != nil {
		return nil, nil, err
	}
	return conn, names, nil
}

// connect opens an SSH connection using SSHFunc or the default infra.ConnectSSH.
func (c *Cluster) connect(ctx context.Context, addr string) (utils.SSHClient, error) {
	connectFn := c.SSHFunc
	if connectFn == nil {
		connectFn = func(ctx context.Context, addr string) (utils.SSHClient, error) {
			return infra.ConnectSSH(ctx, addr, utils.DefaultUser, c.SSHKey)
		}
	}
	ssh, err := connectFn(ctx, addr)
	if err != nil {
		return nil, fmt.Errorf("ssh master: %w", err)
	}
	return ssh, nil
}
