package provider

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
)

// TunnelProvider reconciles a managed outbound tunnel at the provider
// (Cloudflare Tunnel, ngrok) and returns k8s workloads + DNS bindings.
// The tunnel replaces Caddy as the ingress mechanism when
// cfg.Providers.Tunnel is set.
//
// Contract:
//   - Reconcile is idempotent: re-running with identical Routes → no-op.
//   - Tunnel is looked up by deterministic name nvoi-{app}-{env} — same
//     naming-is-the-lookup-key rule as every other nvoi resource.
//   - Tunnel providers never write DNS. All DNS writes flow through
//     DNSProvider.RouteTo() using the IngressBinding returned in TunnelPlan.
type TunnelProvider interface {
	ValidateCredentials(ctx context.Context) error

	// Reconcile upserts the tunnel at the provider for this app+env and
	// returns workloads to apply in the cluster plus DNS bindings to write
	// via the DNS provider. Idempotent.
	Reconcile(ctx context.Context, req TunnelRequest) (*TunnelPlan, error)

	// Delete removes the tunnel at the provider. Idempotent.
	// Providers that model active connectors separately must tear those
	// down before deleting the tunnel itself.
	Delete(ctx context.Context, name string) error

	ListResources(ctx context.Context) ([]ResourceGroup, error)
}

// TunnelRequest is the input to TunnelProvider.Reconcile.
type TunnelRequest struct {
	// Name is the deterministic tunnel name: nvoi-{app}-{env}.
	Name      string
	Namespace string // k8s namespace where services live
	Labels    map[string]string
	// Routes is the cluster-wide set of hostname→service:port mappings.
	// The tunnel handles all services through one provider-side tunnel.
	Routes []TunnelRoute
}

// TunnelRoute maps a public hostname to an in-cluster service.
type TunnelRoute struct {
	Hostname    string // e.g. "api.myapp.com"
	ServiceName string // k8s Service name inside the app namespace
	ServicePort int
	Scheme      string // "http" — upstream is always plain HTTP inside cluster
}

// TunnelPlan is the output of TunnelProvider.Reconcile.
type TunnelPlan struct {
	// Workloads to Apply to the cluster (Deployment + Secret ± ConfigMap).
	Workloads []runtime.Object

	// DNSBindings: hostname → IngressBinding. The reconciler passes each
	// to dns.RouteTo() — the tunnel provider declares the CNAME target,
	// the DNS provider writes the record.
	DNSBindings map[string]IngressBinding
}

// ── Registry ─────────────────────────────────────────────────────────────────

var tunnelRegistry = newRegistry[TunnelProvider]("tunnel")

// RegisterTunnel registers a TunnelProvider factory under a name.
// Called from the provider package's init().
func RegisterTunnel(name string, schema CredentialSchema, factory func(creds map[string]string) TunnelProvider) {
	tunnelRegistry.register(name, schema, factory)
}

// GetTunnelSchema returns the credential schema for a tunnel provider name.
func GetTunnelSchema(name string) (CredentialSchema, error) {
	return tunnelRegistry.getSchema(name)
}

// ResolveTunnel creates a TunnelProvider with pre-resolved credentials.
func ResolveTunnel(name string, creds map[string]string) (TunnelProvider, error) {
	return tunnelRegistry.resolve(name, creds)
}
