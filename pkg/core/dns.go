package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/pkg/infra"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// waitHTTPSFunc is the HTTPS reachability check. Variable for testing.
var waitHTTPSFunc = infra.WaitHTTPS

// ── DNS ───────────────────────────────────────────────────────────────────────

type DNSSetRequest struct {
	Cluster
	DNS     ProviderRef
	Service string
	Domains []string
	Proxy   bool // Cloudflare proxy mode
}

// DNSSet creates/updates DNS A records. DNS only — no Caddy.
func DNSSet(ctx context.Context, req DNSSetRequest) error {
	out := req.Log()

	master, _, _, err := req.Cluster.Master(ctx)
	if err != nil {
		return err
	}

	dns, err := provider.ResolveDNS(req.DNS.Name, req.DNS.Creds)
	if err != nil {
		return err
	}

	ip := master.IPv4
	out.Command("dns", "set", req.Service, "ip", ip, "domains", req.Domains)

	if req.Proxy && req.DNS.Name != "cloudflare" {
		return fmt.Errorf("--proxy requires Cloudflare as DNS provider (current: %s)", req.DNS.Name)
	}

	for _, domain := range req.Domains {
		out.Progress(fmt.Sprintf("ensuring %s → %s", domain, ip))
		if err := dns.EnsureARecord(ctx, domain, ip, req.Proxy); err != nil {
			return fmt.Errorf("dns set %s: %w", domain, err)
		}
		out.Success(domain)
	}

	return nil
}

type DNSDeleteRequest struct {
	Cluster
	DNS     ProviderRef
	Service string
	Domains []string
}

// DNSDelete removes DNS A records. Does NOT touch Caddy — use IngressRemoveRoute for that.
func DNSDelete(ctx context.Context, req DNSDeleteRequest) error {
	out := req.Log()
	out.Command("dns", "delete", req.Service)

	dns, err := provider.ResolveDNS(req.DNS.Name, req.DNS.Creds)
	if err != nil {
		return err
	}

	for _, domain := range req.Domains {
		out.Progress(fmt.Sprintf("deleting %s", domain))
		if err := dns.DeleteARecord(ctx, domain); err != nil {
			return fmt.Errorf("dns delete %s: %w", domain, err)
		}
	}

	// Also remove the Caddy route (keeps teardown working as a single command).
	// This is a convenience — the canonical owner of Caddy is IngressRemoveRoute.
	if req.Provider != "" {
		if err := IngressRemoveRoute(ctx, req.Cluster, req.Service); err != nil {
			out.Warning(fmt.Sprintf("caddy cleanup: %v", err))
		}
	}

	return nil
}

// IngressRemoveRouteRequest holds the params for removing a service's ingress route.
type IngressRemoveRouteRequest struct {
	Cluster
	Service string
}

// IngressRemoveRoute removes a single service's route from the Caddyfile.
// If no routes remain, Caddy and its ConfigMap are deleted entirely.
func IngressRemoveRoute(ctx context.Context, c Cluster, service string) error {
	ssh, names, err := c.SSH(ctx)
	if errors.Is(err, ErrNoMaster) {
		return nil
	}
	if err != nil {
		return nil
	}
	defer ssh.Close()

	ns := names.KubeNamespace()
	existing, _ := kube.GetIngressRoutes(ctx, ssh, ns, names.KubeCaddyConfig())
	routes := removeRoute(existing, service)

	if len(routes) == 0 {
		kube.DeleteByName(ctx, ssh, ns, names.KubeCaddy())
		kube.RunKubectl(ctx, ssh, ns, fmt.Sprintf("delete configmap %s --ignore-not-found", names.KubeCaddyConfig()))
	} else {
		return kube.ApplyCaddyConfig(ctx, ssh, ns, routes, names)
	}
	return nil
}

type DNSListRequest struct {
	DNS    ProviderRef
	Output Output
}

func DNSList(ctx context.Context, req DNSListRequest) ([]provider.DNSRecord, error) {
	dns, err := provider.ResolveDNS(req.DNS.Name, req.DNS.Creds)
	if err != nil {
		return nil, err
	}
	return dns.ListARecords(ctx)
}

// ── Ingress ───────────────────────────────────────────────────────────────────

// IngressRouteArg is a parsed service:domain,domain arg for ingress apply.
type IngressRouteArg struct {
	Service string
	Domains []string
	Proxy   bool
}

// ParseIngressArgs parses "service:domain,domain" or "service:domain,domain:proxy" args.
// Trailing ":proxy" enables per-route proxy mode (alternative to --proxy which is all-or-nothing).
func ParseIngressArgs(args []string) ([]IngressRouteArg, error) {
	var routes []IngressRouteArg
	for _, arg := range args {
		service, rest, ok := strings.Cut(arg, ":")
		if !ok || service == "" || rest == "" {
			return nil, fmt.Errorf("invalid route %q — expected service:domain,domain or service:domain,domain:proxy", arg)
		}

		// Check for trailing :proxy suffix
		proxy := false
		if strings.HasSuffix(rest, ":proxy") {
			proxy = true
			rest = strings.TrimSuffix(rest, ":proxy")
		}

		var domains []string
		for _, d := range strings.Split(rest, ",") {
			d = strings.TrimSpace(d)
			if d != "" {
				domains = append(domains, d)
			}
		}
		if len(domains) == 0 {
			return nil, fmt.Errorf("invalid route %q — no domains", arg)
		}
		routes = append(routes, IngressRouteArg{Service: service, Domains: domains, Proxy: proxy})
	}
	return routes, nil
}

type IngressApplyRequest struct {
	Cluster
	Routes  []IngressRouteArg
	CertPEM string // TLS cert PEM — custom cert instead of ACME (optional)
	KeyPEM  string // TLS key PEM (required if CertPEM is set)
}

// IngressApply builds the full Caddyfile from the given routes and deploys Caddy once.
func IngressApply(ctx context.Context, req IngressApplyRequest) error {
	out := req.Log()

	ssh, names, err := req.Cluster.SSH(ctx)
	if err != nil {
		return err
	}
	defer ssh.Close()

	ns := names.KubeNamespace()
	if err := kube.EnsureNamespace(ctx, ssh, ns); err != nil {
		return err
	}

	out.Command("ingress", "apply", names.KubeCaddy())

	// Build routes from args — resolve each service's port from the cluster
	var routes []kube.IngressRoute
	for _, r := range req.Routes {
		port, err := kube.GetServicePort(ctx, ssh, ns, r.Service)
		if err != nil {
			return fmt.Errorf("service %q has no port — ingress requires a service with --port: %w", r.Service, err)
		}
		routes = append(routes, kube.IngressRoute{
			Service: r.Service,
			Port:    port,
			Domains: r.Domains,
			Proxy:   r.Proxy,
		})
		out.Progress(fmt.Sprintf("%s → %s", r.Service, strings.Join(r.Domains, ", ")))
	}

	if len(routes) == 0 {
		out.Info("no routes — skipping caddy")
		return nil
	}

	// Store TLS cert if provided (custom cert or auto-generated Origin CA)
	hasCert := req.CertPEM != "" && req.KeyPEM != ""
	if hasCert {
		out.Progress("storing TLS certificate")
		if err := kube.UpsertTLSSecret(ctx, ssh, ns, kube.CaddyTLSSecretName, req.CertPEM, req.KeyPEM); err != nil {
			return fmt.Errorf("store cert: %w", err)
		}
		out.Success("certificate stored")
	}

	// If proxy routes requested but no cert provided, check for existing secret
	if !hasCert {
		anyProxy := false
		for _, r := range req.Routes {
			if r.Proxy {
				anyProxy = true
				break
			}
		}
		if anyProxy {
			if kube.TLSSecretExists(ctx, ssh, ns, kube.CaddyTLSSecretName) {
				out.Info("reusing existing Origin CA certificate")
				hasCert = true
			}
		}
	}

	// Custom certs reuse the same Caddy TLS path as proxied routes.
	if hasCert {
		for i := range routes {
			routes[i].Proxy = true
		}
	}

	// Pre-flight: firewall × proxy coherence (after cert override so we check final state)
	finalRoutes := make([]IngressRouteArg, len(req.Routes))
	copy(finalRoutes, req.Routes)
	if hasCert {
		for i := range finalRoutes {
			finalRoutes[i].Proxy = true
		}
	}
	if err := checkFirewallCoherence(ctx, req.Cluster, finalRoutes); err != nil {
		return err
	}

	out.Progress("applying caddy config")
	if err := kube.ApplyCaddyConfig(ctx, ssh, ns, routes, names); err != nil {
		return fmt.Errorf("caddy: %w", err)
	}
	out.Success("caddy ready")

	// Verify domains are reachable
	for _, route := range req.Routes {
		if len(route.Domains) == 0 {
			continue
		}
		firstDomain := route.Domains[0]
		if route.Proxy || hasCert {
			out.Success(fmt.Sprintf("proxied via Cloudflare — https://%s", firstDomain))
			continue
		}

		out.Progress(fmt.Sprintf("waiting for https://%s", firstDomain))
		if err := waitHTTPSFunc(ctx, firstDomain); err != nil {
			return fmt.Errorf("https://%s not reachable: %w", firstDomain, err)
		}
		out.Success(fmt.Sprintf("https://%s live", firstDomain))
	}

	return nil
}

// checkFirewallCoherence validates firewall rules match the proxy mode.
// Proxy + open to all = origin directly reachable, bypassing Cloudflare.
// No proxy + CF-only firewall = ACME (Let's Encrypt) can't reach origin.
func checkFirewallCoherence(ctx context.Context, c Cluster, routes []IngressRouteArg) error {
	prov, err := c.Compute()
	if err != nil {
		return fmt.Errorf("firewall check: %w", err)
	}
	fwNames, err := c.Names()
	if err != nil {
		return fmt.Errorf("firewall check: %w", err)
	}

	rules, err := prov.GetFirewallRules(ctx, fwNames.Firewall())
	if err != nil {
		if errors.Is(err, utils.ErrNotFound) {
			return nil // firewall not created yet — skip coherence check
		}
		return fmt.Errorf("firewall check: %w", err)
	}

	anyProxy := false
	anyDirect := false
	for _, route := range routes {
		if route.Proxy {
			anyProxy = true
		} else {
			anyDirect = true
		}
	}

	// Mixed proxy/direct routes can't be satisfied by any single firewall config
	if anyProxy && anyDirect {
		return fmt.Errorf("cannot mix proxied and non-proxied routes — all routes must be either proxied (through Cloudflare) or direct (ACME). Unify the --proxy setting or split into separate ingress apply invocations")
	}

	has80 := len(rules["80"]) > 0
	has443 := len(rules["443"]) > 0

	if !has80 || !has443 {
		if anyProxy {
			return fmt.Errorf("firewall %s does not have ports 80/443 open — run 'nvoi firewall set cloudflare' first", fwNames.Firewall())
		}
		return fmt.Errorf("firewall %s does not have ports 80/443 open — run 'nvoi firewall set default' first", fwNames.Firewall())
	}

	portOpenToAll := func(port string) bool {
		for _, cidr := range rules[port] {
			if cidr == "0.0.0.0/0" || cidr == "::/0" {
				return true
			}
		}
		return false
	}
	isOpenToAll := portOpenToAll("80") || portOpenToAll("443")

	if anyProxy && isOpenToAll {
		return fmt.Errorf("--proxy with firewall open to all — origin is directly reachable, bypassing Cloudflare. Run 'nvoi firewall set cloudflare' to restrict 80/443 to Cloudflare IPs")
	}

	if anyDirect && !isOpenToAll {
		return fmt.Errorf("firewall restricts 80/443 but --proxy not set — Let's Encrypt ACME validation will fail. Add --proxy or run 'nvoi firewall set default'")
	}

	return nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func removeRoute(routes []kube.IngressRoute, service string) []kube.IngressRoute {
	var result []kube.IngressRoute
	for _, r := range routes {
		if r.Service != service {
			result = append(result, r)
		}
	}
	return result
}
