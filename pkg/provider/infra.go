package provider

import (
	"context"
	"errors"
	"io"

	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// ErrNotImplemented is returned by provider methods that aren't wired yet.
// During the InfraProvider rollout, providers stage their interface
// satisfaction by returning this from Bootstrap / TeardownOrphans /
// Teardown / LiveSnapshot until the orchestration relocation lands.
// Production providers must never return this once the refactor completes.
var ErrNotImplemented = errors.New("infra provider: not implemented")

// ErrNotBootstrapped is returned by Connect / NodeShell when the
// provider can't find the infra it expects (no master server, no
// sandbox, no managed cluster). CLI dispatch surfaces this with
// "run nvoi deploy first." Distinct from ErrNotImplemented (the
// provider works, the cluster doesn't exist yet).
var ErrNotBootstrapped = errors.New("infra not provisioned — run `nvoi deploy` first")

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
	// Connect attaches to existing infra and returns a working kube
	// client. READ-ONLY contract — MUST NOT create, update, or delete
	// any provider resource. If infra is absent, returns
	// ErrNotBootstrapped. Cost target: 1 lookup per resource type +
	// 1 SSH dial + 1 kubeconfig fetch (≤500ms on existing Hetzner).
	//
	// Called by: Cluster.Kube / Cluster.SSH on-demand path — every CLI
	// command except `nvoi deploy` / `nvoi teardown`.
	Connect(ctx context.Context, dc *BootstrapContext) (*kube.Client, error)

	// Bootstrap converges the provider's own resources to whatever shape
	// is required to yield a working kube client, then tail-calls
	// Connect. For IaaS, that's firewalls + network + servers + k3s
	// install + volumes + SSH. For managed k8s, it's an authn handshake.
	// For sandbox backends, a sandbox upsert. WRITE contract — drift is
	// reconciled, missing resources created, firewall rules applied.
	// Idempotent but not read-only.
	//
	// Called by: reconcile.Deploy only.
	Bootstrap(ctx context.Context, dc *BootstrapContext) (*kube.Client, error)

	// LiveSnapshot returns the provider's view of live infra resources
	// (servers, volumes, firewalls + per-firewall rule contents) for
	// orphan-detection input AND plan-mode diffing. Returns nil when no
	// resources exist (first deploy). Used by TeardownOrphans (orphan
	// detection) and PlanInfra (cfg-vs-live diff).
	LiveSnapshot(ctx context.Context, dc *BootstrapContext) (*LiveSnapshot, error)

	// PlanInfra returns the diff between desired infra state (dc.Cfg)
	// and the current LiveSnapshot. READ-ONLY — never mutates. Empty
	// slice means no infra changes; the deploy can skip the loud
	// per-resource ensure path and use Connect instead of Bootstrap.
	//
	// Default impl: each provider calls its own LiveSnapshot, then
	// delegates to the shared ComputeInfraPlan helper. Provider-side
	// PlanInfra exists as an interface method (not a free function) so
	// future provider kinds (sandbox, managed-k8s) can short-circuit
	// with an empty plan when the concept doesn't apply.
	PlanInfra(ctx context.Context, dc *BootstrapContext) ([]PlanEntry, error)

	// TeardownOrphans removes infra no longer referenced by the desired
	// state. IaaS: drain and delete orphan servers / firewalls / volumes.
	// Single-unit providers (sandbox, managed-k8s without cluster-creation
	// ownership): no-op. Called after workload reconcile so live workloads
	// have already moved off the resources marked for removal.
	//
	// The provider does its OWN live-state lookup (no live param threaded
	// through reconcile) — keeps reconcile free of provider-shape data
	// types and lets each provider use whatever lookup is most efficient
	// for its API.
	TeardownOrphans(ctx context.Context, dc *BootstrapContext) error

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

	// SSHToNode returns an SSH client for a SPECIFIC node identified by
	// its YAML-declared server short name (e.g. "master", "db-master",
	// "worker-1"). Distinct from NodeShell which always returns the
	// master's SSH: SSHToNode lets callers that need host-level access to
	// a non-master node (the postgres provider's ZFS prepare-node phase,
	// for example) reach the right machine.
	//
	// Providers without host SSH (managed k8s) return (nil,
	// ErrNotImplemented). Hetzner/AWS/Scaleway dial the server's public
	// IPv4 directly using the same SSH key NodeShell uses. Callers must
	// Close() the returned client.
	SSHToNode(ctx context.Context, dc *BootstrapContext, serverName string) (utils.SSHClient, error)

	// ValidateCredentials probes the backend's API at startup so a
	// misconfigured provider fails loudly before reconcile begins.
	ValidateCredentials(ctx context.Context) error

	// Close releases any provider-internal resources (cached SSH
	// connections, HTTP transports holding sockets open, etc.). Idempotent.
	// Called by the CLI at the end of every command that opened a
	// provider — without it, SSH-tunneled providers leak file descriptors.
	Close() error

	// ArchForType returns the CPU architecture ("amd64" or "arm64") for the
	// given server/instance type name. Pure — no API calls, no credentials
	// needed. Used by the build pass to set --platform on docker buildx so
	// the image arch always matches the target server.
	//   hetzner: "cax*" → arm64 (Ampere Altra), everything else → amd64.
	//   aws:     "a1.*", "t4g.*", "m6g.*", "c7g.*", etc. → arm64.
	//   scaleway: "AMP2*", "COPARM1*" → arm64, everything else → amd64.
	ArchForType(serverType string) string

	// ProvisionBuilders converges role: builder servers + their per-server
	// cache volumes + the shared builder firewall. Idempotent: existing
	// builders are lookup-only (no re-install), missing ones are created.
	// Runs BEFORE Bootstrap on the SSH-build dispatch path — the CLI calls
	// ProvisionBuilders first (so the target exists), fetches BuilderTargets,
	// then SSH-dispatches the deploy into the builder where Bootstrap runs
	// against the master/workers.
	//
	// Providers that don't support builders (managed k8s, sandbox) return
	// nil when no role: builder servers are declared and an explicit error
	// otherwise ("<provider> does not support role: builder").
	ProvisionBuilders(ctx context.Context, dc *BootstrapContext) error

	// BuilderTargets returns the SSH addressable endpoints of every
	// role: builder server this provider manages for the current cluster.
	// Read-only. Called by the CLI on the build-dispatch path to hand the
	// SSH BuildProvider a target list — same ownership pattern as NodeShell.
	//
	//   IaaS: one entry per role: builder server (IPv4 + DefaultUser).
	//   Managed/sandbox: (nil, nil). Callers feature-gate on empty.
	BuilderTargets(ctx context.Context, dc *BootstrapContext) ([]BuilderTarget, error)
}

// BuilderTarget is the SSH-reachable address of one role: builder server.
// The SSH BuildProvider dials Host:22 as User, authenticates with the
// operator's SSH private key (passed through BuildRequest.SSHKey), and
// runs `git clone` + `docker buildx build --push` on the builder.
type BuilderTarget struct {
	// Name is the full provider-side server name (e.g.
	// "nvoi-myapp-prod-builder-1"). Diagnostic only — not parsed.
	Name string
	// Host is the routable address (public IPv4 for IaaS builders). The SSH
	// BuildProvider dials Host + ":22".
	Host string
	// User is the login user — always utils.DefaultUser today ("deploy"),
	// per RenderBuilderCloudInit. Carried explicitly so future non-Ubuntu
	// images can diverge without touching the SSH BuildProvider.
	User string
}

// IngressBinding tells the DNS provider how to route a domain. The DNS
// provider picks its own representation (A / AAAA / CNAME / ALIAS /
// proxied) using DNSType as a hint.
type IngressBinding struct {
	DNSType   string // "A" | "AAAA" | "CNAME"
	DNSTarget string
	// Proxied, when true, requests that the DNS provider enable its own
	// proxy layer for the record. On Cloudflare this is the orange-cloud
	// flag, which is REQUIRED for CNAME records pointing at cfargotunnel.com
	// — without it the subdomain has no public IPs and traffic never reaches
	// the Cloudflare edge, causing ERR_CONNECTION_REFUSED.
	Proxied bool
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
// Cluster identity (App, Env) drives naming. ProviderName is the name the
// provider was registered under (e.g. "hetzner" in production, "test-X"
// under fakes); some downstream helpers re-resolve the provider via the
// registry and need it. Credentials are pre-resolved (the reconciler ran
// them through CredentialSource at the cmd/ boundary). SSHKey is the
// operator's private key bytes — providers that mint their own SSH
// credentials (token-auth gateways) ignore it. DeployHash is the per-
// deploy stamp inherited from cluster-wide state.
//
// Output is the event sink — providers emit progress through it; never
// stdout, never log. Cfg is opaque to pkg/provider; concrete providers
// type-assert to whatever view they need from their own package.
type BootstrapContext struct {
	App          string
	Env          string
	ProviderName string
	Credentials  map[string]string
	SSHKey       []byte
	DeployHash   string
	Output       EventSink
	Cfg          ProviderConfigView

	// SSHDial overrides the production infra.ConnectSSH dial. When non-nil,
	// providers call this to open SSH connections (Bootstrap → master /
	// Teardown → per-server unmount). Tests inject a closure returning a
	// canned MockSSH; production leaves it nil and providers fall back to
	// infra.ConnectSSH directly.
	SSHDial func(ctx context.Context, addr string) (utils.SSHClient, error)

	// MasterKube, when non-nil, is returned by Bootstrap instead of dialing
	// the kube tunnel. Test scaffolding pre-injects a KubeFake here so
	// invariant tests can exercise the full reconcile.Deploy path without
	// the SSH-tunneled apiserver dance. Production leaves it nil; Bootstrap
	// then builds a real *kube.Client over the master SSH connection.
	//
	// Mirror of the existing Cluster.NodeShell / Cluster.MasterKube
	// "borrowed reference" pattern in pkg/core/cluster.go: when the
	// reconciler/test owns the connection, the provider returns it; when
	// it's nil, the provider creates and returns a fresh one.
	MasterKube *kube.Client
}

// LiveSnapshot is the orphan-detection input: what the provider sees in
// the world right now (server names, volume names, firewall names +
// per-firewall current rule contents). Populated by the reconciler from
// DescribeLive.
//
// FirewallRules is keyed by full firewall name (matching entries in
// Firewalls) and holds the current public/custom port rules. Internal
// cluster ports (6443/10250/8472/5000) and SSH are filtered out by the
// provider's GetFirewallRules — they're nvoi-managed and never appear
// here. nil for a given firewall name means "unable to fetch" or
// "base-only rules"; the planner treats both the same (no diff).
type LiveSnapshot struct {
	Servers       []string
	ServerDisk    map[string]int
	Firewalls     []string
	FirewallRules map[string]PortAllowList
	Volumes       []string
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
	// TunnelProvider returns the configured tunnel provider name
	// ("cloudflare", "ngrok") or empty string when no tunnel is configured.
	// Bootstrap uses this to decide whether 80/443 should be auto-opened on
	// the master firewall (Caddy mode) or kept closed (tunnel mode).
	TunnelProvider() string
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

// EventSink is the output interface providers use. Identical-shape mirror
// of pkg/core.Output (defined here so pkg/provider stays free of pkg/core).
// Writer() is the streaming-output sink used by pkg/infra helpers
// (k3s install, swap, volume mount) — providers route their progress
// through it without owning a buffer.
type EventSink interface {
	Command(command, action, name string, extra ...any)
	Progress(string)
	Success(string)
	Warning(string)
	Info(string)
	Error(error)
	Writer() io.Writer
}

// ── Registry ──────────────────────────────────────────────────────────────────

var infraRegistry = newRegistry[InfraProvider]("infra")

// RegisterInfra registers an InfraProvider factory under a name. Called
// from the provider's init().
func RegisterInfra(name string, schema CredentialSchema, factory func(creds map[string]string) InfraProvider) {
	infraRegistry.register(name, schema, factory)
}

// GetInfraSchema returns the credential schema for an infra provider name.
func GetInfraSchema(name string) (CredentialSchema, error) {
	return infraRegistry.getSchema(name)
}

// ResolveInfra creates an infra provider with pre-resolved credentials.
// Credentials must already be fully resolved (flag → env / source done by caller).
func ResolveInfra(name string, creds map[string]string) (InfraProvider, error) {
	return infraRegistry.resolve(name, creds)
}
