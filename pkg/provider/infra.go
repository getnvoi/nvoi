package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// ErrNotImplemented is returned by provider methods that aren't wired yet.
// During the InfraProvider rollout, providers stage their interface
// satisfaction by returning this from Bootstrap / TeardownOrphans /
// Teardown / LiveSnapshot until the orchestration relocation lands.
// Production providers must never return this once the refactor completes.
var ErrNotImplemented = errors.New("infra provider: not implemented")

// InfraProvider is the narrow contract every infrastructure backend
// satisfies. The single load-bearing promise: Bootstrap returns a working
// *kube.Client. Everything else is either gated capability (NodeShell,
// IngressBinding) or provider-private bookkeeping (LiveSnapshot,
// TeardownOrphans, Teardown, ConsumesBlocks, ValidateConfig).
//
// Adding a new infra backend = implementing this interface. Reconcile
// never branches on "what kind of provider is this" — the gates above
// (HasPublicIngress, returned-nil NodeShell, ConsumesBlocks) carry every
// distinction.
//
// Why this exists: the previous ComputeProvider mixed IaaS-specific ops
// (servers, firewalls, volumes, networks, block devices) into the public
// surface. For non-IaaS targets (managed k8s, sandbox runtimes), 11 of 16
// methods became no-ops or fiction. This interface keeps only what every
// backend genuinely owes the reconciler.
type InfraProvider interface {
	// Bootstrap converges the provider's own resources to whatever shape
	// is required to yield a working kube client. For IaaS, that's
	// firewalls + network + servers + k3s install + volumes + SSH. For
	// managed k8s, it's an authn handshake. For sandbox backends, a
	// sandbox upsert. Caller treats this as opaque. Idempotent.
	Bootstrap(ctx context.Context, dc *BootstrapContext) (*kube.Client, error)

	// LiveSnapshot returns the provider's view of live infra resources
	// (servers, volumes, firewalls) for orphan-detection input. Returns
	// nil when no resources exist (first deploy). Used by reconcile's
	// DescribeLive to feed TeardownOrphans.
	LiveSnapshot(ctx context.Context, dc *BootstrapContext) (*LiveSnapshot, error)

	// TeardownOrphans removes infra no longer referenced by the desired
	// state. IaaS: drain and delete orphan servers / firewalls / volumes.
	// Single-unit providers (sandbox, managed-k8s without cluster-creation
	// ownership): no-op. Called after workload reconcile so live workloads
	// have already moved off the resources marked for removal.
	TeardownOrphans(ctx context.Context, dc *BootstrapContext, live *LiveSnapshot) error

	// Teardown hard-nukes every provider resource owned by this cluster
	// (matched by labels). Backs `nvoi teardown` / `bin/destroy`. When
	// deleteVolumes is false, persistent volumes are detached but
	// preserved (matches existing --delete-volumes contract). Network
	// always destroyed; storage handled separately by the caller.
	Teardown(ctx context.Context, dc *BootstrapContext, deleteVolumes bool) error

	// IngressBinding resolves how external traffic reaches the given
	// service. Called only when HasPublicIngress() is true AND no tunnel
	// provider is configured. The return value flows directly into
	// DNSProvider.RouteTo. dc carries the cluster identity (App/Env)
	// the provider needs to look up its own resources by label.
	//   IaaS:    { DNSType: "A",     DNSTarget: master.IPv4 }
	//   Managed: { DNSType: "CNAME", DNSTarget: lb.external.hostname }
	IngressBinding(ctx context.Context, dc *BootstrapContext, svc ServiceTarget) (IngressBinding, error)

	// HasPublicIngress reports whether this infra exposes a reachable
	// public endpoint Caddy can bind to. IaaS with public IPs: true.
	// Sandbox without a public IP: false. The validator uses this to
	// reject `domains:` when there's no path for traffic to land.
	HasPublicIngress() bool

	// ConsumesBlocks declares which top-level YAML blocks this provider
	// reads. Validator rejects blocks not in this set.
	//   IaaS:    ["servers", "firewall", "volumes"]
	//   Sandbox: ["sandbox"]
	ConsumesBlocks() []string

	// ValidateConfig is the provider's chance to enforce its own
	// invariants beyond the generic validator. Runs late, after
	// ConsumesBlocks gating. Returns nil on success.
	ValidateConfig(cfg ProviderConfigView) error

	// ListResources returns every provider-managed resource the `nvoi
	// resources` command should display.
	ListResources(ctx context.Context) ([]ResourceGroup, error)

	// NodeShell returns an SSH client for ad-hoc host shell access used
	// by `nvoi ssh`. Providers that don't expose node shell (managed k8s
	// behind cloud authn) return (nil, nil). The CLI feature-gates on
	// nil with an actionable error.
	//
	// On the deploy path, NodeShell is called after Bootstrap; SSH-tunneled
	// providers may return the same connection Bootstrap dialed for the
	// kube tunnel (cached on the receiver). On the dispatch path
	// (`nvoi ssh` without a preceding deploy), Bootstrap hasn't run, so
	// the provider dials fresh.
	NodeShell(ctx context.Context, dc *BootstrapContext) (utils.SSHClient, error)

	// ValidateCredentials probes the backend's API at startup so a
	// misconfigured provider fails loudly before reconcile begins.
	ValidateCredentials(ctx context.Context) error

	// Close releases any provider-internal resources (cached SSH
	// connections, HTTP transports holding sockets open, etc.). Idempotent.
	// Called by the CLI at the end of every command that opened a
	// provider — without it, SSH-tunneled providers leak file descriptors.
	Close() error
}

// IngressBinding tells the DNS provider how to route a domain. The DNS
// provider picks its own representation (A / AAAA / CNAME / ALIAS /
// proxied) using DNSType as a hint.
type IngressBinding struct {
	DNSType   string // "A" | "AAAA" | "CNAME"
	DNSTarget string
}

// ServiceTarget is the slice of service info the InfraProvider needs to
// compute an IngressBinding. Lives in pkg/provider so InfraProvider can
// stay free of dependencies on internal/config (layer rule: pkg/ never
// imports internal/). Reconcile converts config.ServiceDef to this.
type ServiceTarget struct {
	Name string
	Port int
}

// BootstrapContext carries the per-deploy data InfraProvider methods
// need without coupling pkg/provider to internal/config or pkg/core.
// Populated by the reconciler before calling Bootstrap.
//
// Cluster identity (App, Env) drives naming. Credentials are pre-resolved
// (the reconciler ran them through CredentialSource at the cmd/ boundary).
// SSHKey is the operator's private key bytes — providers that mint their
// own SSH credentials (e.g. token-auth gateways) ignore it. DeployHash is
// the per-deploy stamp inherited from cluster-wide state.
//
// Output is the event sink — providers emit progress through it; never
// stdout, never log. Cfg is opaque to pkg/provider; concrete providers
// type-assert to whatever view they need from their own package.
type BootstrapContext struct {
	App         string
	Env         string
	Credentials map[string]string
	SSHKey      []byte
	DeployHash  string
	Output      EventSink
	Cfg         ProviderConfigView
}

// LiveSnapshot is the orphan-detection input: what the provider sees in
// the world right now (server names, volume names, firewall names).
// Populated by the reconciler from DescribeLive.
type LiveSnapshot struct {
	Servers    []string
	ServerDisk map[string]int
	Firewalls  []string
	Volumes    []string
}

// ProviderConfigView is the projection of AppConfig the InfraProvider
// reads. Concrete providers type-assert to whatever extra YAML they
// consume (declared via ConsumesBlocks). Defined as an interface so
// pkg/provider stays independent of internal/config — the reconciler
// passes a view that wraps *config.AppConfig.
type ProviderConfigView interface {
	AppName() string
	EnvName() string
	ServerDefs() []ServerSpec
	FirewallRules() []string
	VolumeDefs() []VolumeSpec
	ServiceDefs() []ServiceSpec
	DomainsByService() map[string][]string
}

// ServerSpec is the provider-facing view of a server entry.
type ServerSpec struct {
	Name   string
	Type   string
	Region string
	Role   string
	Disk   int
}

// VolumeSpec is the provider-facing view of a volume entry.
type VolumeSpec struct {
	Name      string
	Size      int
	Server    string
	MountPath string
}

// ServiceSpec is the minimal service view a provider needs from
// AppConfig — enough to emit IngressBindings.
type ServiceSpec struct {
	Name string
	Port int
}

// EventSink is the narrow output interface providers use. Mirrors the
// methods on pkg/core.Output that providers actually call. Defined here
// (not imported from pkg/core) to keep pkg/provider free of pkg/core.
type EventSink interface {
	Command(command, action, name string, extra ...any)
	Progress(string)
	Success(string)
	Warning(string)
	Info(string)
	Error(error)
}

// ── Registry ──────────────────────────────────────────────────────────────────

type infraEntry struct {
	schema  CredentialSchema
	factory func(creds map[string]string) InfraProvider
}

var infraProviders = map[string]infraEntry{}

// RegisterInfra registers an InfraProvider factory under a name. Called
// from the provider's init().
func RegisterInfra(name string, schema CredentialSchema, factory func(creds map[string]string) InfraProvider) {
	infraProviders[name] = infraEntry{schema: schema, factory: factory}
}

// GetInfraSchema returns the credential schema for an infra provider name.
func GetInfraSchema(name string) (CredentialSchema, error) {
	entry, ok := infraProviders[name]
	if !ok {
		return CredentialSchema{}, fmt.Errorf("unsupported infra provider: %q", name)
	}
	return entry.schema, nil
}

// ResolveInfra creates an infra provider with pre-resolved credentials.
// Credentials must already be fully resolved (flag → env / source done by caller).
func ResolveInfra(name string, creds map[string]string) (InfraProvider, error) {
	entry, ok := infraProviders[name]
	if !ok {
		return nil, fmt.Errorf("unsupported infra provider: %q", name)
	}
	if err := entry.schema.Validate(creds); err != nil {
		return nil, err
	}
	return entry.factory(creds), nil
}
