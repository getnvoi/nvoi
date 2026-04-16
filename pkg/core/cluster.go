package core

import (
	"context"
	"io"

	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// ProviderRef pairs a provider name with its resolved credentials.
// Used for secondary providers (DNS, storage) on request types.
type ProviderRef struct {
	Name  string
	Creds map[string]string
}

// ConnectSSH dials SSH to a server address. Callers provide the right implementation:
// bootstrap passes infra.ConnectSSH wired with the SSH key, agent passes WorkerAccess.
type ConnectSSH func(ctx context.Context, addr string) (utils.SSHClient, error)

// Cluster identifies a deployment target: app + env + compute provider + SSH key.
// Embedded by every request type. Pure identity — no per-request state, no SSH.
type Cluster struct {
	AppName     string
	Env         string
	Provider    string
	Credentials map[string]string
	SSHKey      []byte

	// Kube is the client-go k8s API client. When set, all kubectl operations
	// go through it — no shell commands, no kubectl binary.
	// Agent: direct to localhost:6443. CLI bootstrap: SSH-tunneled.
	Kube *kube.KubeClient

	// MasterIP is the master node's public IPv4. Set at construction.
	// Agent: resolved at startup. Bootstrap: resolved after finding master.
	MasterIP string

	// MasterPrivateIP is the master node's private IP in the cluster network.
	// Used for registry address, k3s join, etc.
	MasterPrivateIP string
}

// log returns the Output, falling back to a no-op if nil.
// Every request function calls this as its first line.
func log(o Output) Output {
	if o != nil {
		return o
	}
	return NopOutput{}
}

// NopOutput silently discards all events.
type NopOutput struct{}

func (NopOutput) Command(string, string, string, ...any) {}
func (NopOutput) Progress(string)                        {}
func (NopOutput) Success(string)                         {}
func (NopOutput) Warning(string)                         {}
func (NopOutput) Info(string)                            {}
func (NopOutput) Error(error)                            {}
func (NopOutput) Writer() io.Writer                      { return io.Discard }

// Names resolves the naming convention for this cluster.
func (c *Cluster) Names() (*utils.Names, error) {
	return utils.NewNames(c.AppName, c.Env)
}

// Compute resolves the compute provider.
func (c *Cluster) Compute() (provider.ComputeProvider, error) {
	return provider.ResolveCompute(c.Provider, c.Credentials)
}
