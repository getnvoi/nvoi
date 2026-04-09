package core

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/getnvoi/nvoi/pkg/infra"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// waitHTTPSFunc is the HTTPS reachability check. Variable for testing.
var waitHTTPSFunc = infra.WaitHTTPS

// ── Types ────────────────────────────────────────────────────────────────────

// IngressRouteArg is a parsed service:domain,domain arg.
type IngressRouteArg struct {
	Service     string
	Domains     []string
	EdgeProxied bool
}

type ingressExposureMode string
type ingressTLSMode string

const (
	exposureDirect      ingressExposureMode = "direct"
	exposureEdgeProxied ingressExposureMode = "edge_proxied"

	tlsACME       ingressTLSMode = "acme"
	tlsProvided   ingressTLSMode = "provided"
	tlsEdgeOrigin ingressTLSMode = "edge_origin"
)

type IngressSetRequest struct {
	Cluster
	DNS          ProviderRef
	Route        IngressRouteArg // single route to add/update
	EdgeProvider string
	CertPEM      string
	KeyPEM       string
}

type IngressDeleteRequest struct {
	Cluster
	DNS   ProviderRef
	Route IngressRouteArg // route to remove
}

// ── ParseIngressArgs ────────────────────────────────────────────────────────

// ParseIngressArgs parses "service:domain,domain" args.
func ParseIngressArgs(args []string) ([]IngressRouteArg, error) {
	var routes []IngressRouteArg
	for _, arg := range args {
		service, domainPart, ok := strings.Cut(arg, ":")
		if !ok || service == "" || domainPart == "" {
			return nil, fmt.Errorf("invalid route %q — expected service:domain,domain", arg)
		}
		var domains []string
		for _, d := range strings.Split(domainPart, ",") {
			d = strings.TrimSpace(d)
			if d != "" {
				domains = append(domains, d)
			}
		}
		if len(domains) == 0 {
			return nil, fmt.Errorf("invalid route %q — no domains", arg)
		}
		routes = append(routes, IngressRouteArg{Service: service, Domains: domains})
	}
	return routes, nil
}

// ── IngressSet ──────────────────────────────────────────────────────────────

// IngressSet adds or updates a single ingress route. Reads the current Caddyfile,
// merges the new route, resolves TLS material for the full domain set, and redeploys Caddy.
func IngressSet(ctx context.Context, req IngressSetRequest) error {
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

	out.Command("ingress", "set", req.Route.Service, "domains", req.Route.Domains)

	// Resolve the service port from the cluster.
	port, err := kube.GetServicePort(ctx, ssh, ns, req.Route.Service)
	if err != nil {
		return fmt.Errorf("service %q has no port — ingress requires a service with --port: %w", req.Route.Service, err)
	}

	// Resolve TLS mode.
	tlsMode := resolveTLSMode(req.Route.EdgeProxied, req.CertPEM, req.KeyPEM)

	// Read current routes and merge.
	currentRoutes, _ := kube.GetIngressRoutes(ctx, ssh, ns, names.KubeCaddyConfig())
	merged := mergeRoute(currentRoutes, kube.IngressRoute{
		Service:      req.Route.Service,
		Port:         port,
		Domains:      req.Route.Domains,
		UseTLSSecret: tlsMode != tlsACME,
	})

	// Exposure mode — all routes must be the same.
	exposure := exposureDirect
	if req.Route.EdgeProxied {
		exposure = exposureEdgeProxied
	}

	if err := checkFirewallCoherence(ctx, req.Cluster, exposure); err != nil {
		return err
	}

	// Resolve TLS material for the full merged domain set.
	allDomains := kubeRouteDomains(merged)
	certPEM, keyPEM, certID, err := resolveTLSMaterial(ctx, req.DNS, req.EdgeProvider, req.CertPEM, req.KeyPEM, tlsMode, allDomains, out, ssh, ns)
	if err != nil {
		return err
	}

	// Store TLS cert.
	hasCert := certPEM != "" && keyPEM != ""
	if hasCert {
		out.Progress("storing TLS certificate")
		var annotations map[string]string
		if certID != "" {
			annotations = map[string]string{utils.OriginCAAnnotation: certID}
		}
		if err := kube.UpsertTLSSecret(ctx, ssh, ns, kube.CaddyTLSSecretName, certPEM, keyPEM, annotations); err != nil {
			return fmt.Errorf("store cert: %w", err)
		}
		out.Success("certificate stored")
	} else {
		// ACME mode — clear any leftover Origin CA secret.
		if _, err := kube.RunKubectl(ctx, ssh, ns, fmt.Sprintf("delete secret %s --ignore-not-found", kube.CaddyTLSSecretName)); err != nil {
			return fmt.Errorf("clear tls secret: %w", err)
		}
	}

	out.Progress("applying caddy config")
	if err := kube.ApplyCaddyConfig(ctx, ssh, ns, merged, names); err != nil {
		return fmt.Errorf("caddy: %w", err)
	}
	out.Success("caddy ready")

	// Verify reachability.
	firstDomain := req.Route.Domains[0]
	if exposure == exposureEdgeProxied {
		out.Success(fmt.Sprintf("edge proxied — https://%s", firstDomain))
	} else {
		out.Progress(fmt.Sprintf("waiting for https://%s", firstDomain))
		if err := waitHTTPSFunc(ctx, firstDomain); err != nil {
			return fmt.Errorf("https://%s not reachable: %w", firstDomain, err)
		}
		out.Success(fmt.Sprintf("https://%s live", firstDomain))
	}

	return nil
}

// ── IngressDelete ───────────────────────────────────────────────────────────

// IngressDelete removes a single ingress route.
//
// When routes remain after removal: remove the route from Caddyfile, redeploy Caddy.
// No cert operations — the existing cert stays, next `ingress set` reissues if domains changed.
//
// When no routes remain (last route deleted):
//  1. Revoke Origin CA cert at Cloudflare (--cloudflare-managed only).
//     Two lookup paths: annotation from cluster (fast), or FindCertByHostnames from args (no SSH).
//  2. Wipe local: deployment, configmap, TLS secret.
//
// ErrNoMaster with --cloudflare-managed: still revokes at Cloudflare via hostname lookup, skips local.
// ErrNoMaster without --cloudflare-managed: "cluster gone", done.
func IngressDelete(ctx context.Context, req IngressDeleteRequest) error {
	out := req.Log()
	out.Command("ingress", "delete", req.Route.Service, "domains", req.Route.Domains)

	// Try SSH to cluster.
	ssh, names, sshErr := req.Cluster.SSH(ctx)
	if sshErr != nil {
		if errors.Is(sshErr, ErrNoMaster) {
			// Cluster gone. If cloudflare-managed, still try to revoke at Cloudflare.
			if req.DNS.Name == "cloudflare" {
				if err := revokeByHostnames(ctx, req, out); err != nil {
					return err
				}
			}
			out.Success("cluster gone — local resources already absent")
			return nil
		}
		return sshErr
	}
	defer ssh.Close()

	ns := names.KubeNamespace()
	if err := kube.EnsureNamespace(ctx, ssh, ns); err != nil {
		return err
	}

	// Read current routes, remove the target service.
	currentRoutes, _ := kube.GetIngressRoutes(ctx, ssh, ns, names.KubeCaddyConfig())
	remaining := removeRoute(currentRoutes, req.Route.Service)

	if len(remaining) > 0 {
		// Routes remain — just remove the route, redeploy. No cert operations.
		out.Progress(fmt.Sprintf("removing route, %d remaining", len(remaining)))
		if err := kube.ApplyCaddyConfig(ctx, ssh, ns, remaining, names); err != nil {
			return fmt.Errorf("caddy: %w", err)
		}
		out.Success(fmt.Sprintf("caddy updated (%d route(s) remaining)", len(remaining)))
		return nil
	}

	// Last route — revoke cert at Cloudflare, then wipe local.
	if req.DNS.Name == "cloudflare" {
		// Fast path: read cert ID from annotation.
		certID := kube.GetTLSSecretAnnotation(ctx, ssh, ns, kube.CaddyTLSSecretName, utils.OriginCAAnnotation)
		if certID != "" {
			out.Progress(fmt.Sprintf("revoking Origin CA certificate %s", certID))
			if err := revokeOriginCertFunc(ctx, req.DNS.Creds["api_key"], certID); err != nil {
				return fmt.Errorf("revoke Origin CA cert %s: %w — local resources preserved, retry after fixing", certID, err)
			}
			out.Success("Origin CA certificate revoked")
		} else {
			// Fallback: find by hostname match at Cloudflare.
			if err := revokeByHostnames(ctx, req, out); err != nil {
				return err
			}
		}
	}

	out.Progress("removing caddy ingress")
	if err := deleteAllIngress(ctx, ssh, ns, names); err != nil {
		return err
	}
	out.Success("ingress removed")
	return nil
}

// revokeByHostnames finds and revokes the Origin CA cert at Cloudflare by hostname match.
// Used when the annotation is unavailable (legacy or cluster gone).
func revokeByHostnames(ctx context.Context, req IngressDeleteRequest, out Output) error {
	apiKey := req.DNS.Creds["api_key"]
	zoneID := req.DNS.Creds["zone_id"]
	if zoneID == "" || len(req.Route.Domains) == 0 {
		return nil
	}
	domains := append([]string(nil), req.Route.Domains...)
	sort.Strings(domains)

	out.Progress("finding Origin CA certificate at Cloudflare")
	found, err := findOriginCertFunc(ctx, apiKey, zoneID, domains)
	if err != nil {
		return fmt.Errorf("find Origin CA cert: %w — local resources preserved, retry after fixing", err)
	}
	if found != nil {
		out.Progress(fmt.Sprintf("revoking Origin CA certificate %s", found.ID))
		if err := revokeOriginCertFunc(ctx, apiKey, found.ID); err != nil {
			return fmt.Errorf("revoke Origin CA cert %s: %w — local resources preserved, retry after fixing", found.ID, err)
		}
		out.Success("Origin CA certificate revoked")
	} else {
		out.Success("no matching Origin CA certificate at Cloudflare")
	}
	return nil
}

// ── Helpers ─────────────────────────────────────────────────────────────────

// mergeRoute replaces or appends a route in the current set, matched by service name.
func mergeRoute(current []kube.IngressRoute, route kube.IngressRoute) []kube.IngressRoute {
	merged := make([]kube.IngressRoute, 0, len(current)+1)
	replaced := false
	for _, r := range current {
		if r.Service == route.Service {
			merged = append(merged, route)
			replaced = true
		} else {
			merged = append(merged, r)
		}
	}
	if !replaced {
		merged = append(merged, route)
	}
	return merged
}

// removeRoute removes the route for the given service name.
func removeRoute(current []kube.IngressRoute, service string) []kube.IngressRoute {
	var remaining []kube.IngressRoute
	for _, r := range current {
		if r.Service != service {
			remaining = append(remaining, r)
		}
	}
	return remaining
}

func kubeRouteDomains(routes []kube.IngressRoute) []string {
	var domains []string
	for _, r := range routes {
		domains = append(domains, r.Domains...)
	}
	sort.Strings(domains)
	return domains
}

func deleteAllIngress(ctx context.Context, ssh utils.SSHClient, ns string, names *utils.Names) error {
	if err := kube.DeleteByName(ctx, ssh, ns, names.KubeCaddy()); err != nil {
		return err
	}
	if _, err := kube.RunKubectl(ctx, ssh, ns, fmt.Sprintf("delete configmap %s --ignore-not-found", names.KubeCaddyConfig())); err != nil {
		return err
	}
	if _, err := kube.RunKubectl(ctx, ssh, ns, fmt.Sprintf("delete secret %s --ignore-not-found", kube.CaddyTLSSecretName)); err != nil {
		return err
	}
	return nil
}

func checkFirewallCoherence(ctx context.Context, c Cluster, exposure ingressExposureMode) error {
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

	has80 := len(rules["80"]) > 0
	has443 := len(rules["443"]) > 0

	if !has80 || !has443 {
		if exposure == exposureEdgeProxied {
			return fmt.Errorf("firewall %s does not have ports 80/443 open for the configured edge overlay — run 'nvoi firewall set cloudflare' first", fwNames.Firewall())
		}
		return fmt.Errorf("firewall %s does not have ports 80/443 open — run 'nvoi firewall set default' first", fwNames.Firewall())
	}

	isOpenToAll := false
	for _, port := range []string{"80", "443"} {
		for _, cidr := range rules[port] {
			if cidr == "0.0.0.0/0" || cidr == "::/0" {
				isOpenToAll = true
				break
			}
		}
	}

	if exposure == exposureEdgeProxied && isOpenToAll {
		return fmt.Errorf("edge overlay with firewall open to all leaves the origin directly reachable. Run 'nvoi firewall set cloudflare' to restrict 80/443 to Cloudflare IPs")
	}

	if exposure == exposureDirect && !isOpenToAll {
		return fmt.Errorf("firewall restricts 80/443 but ingress exposure is direct — remove the edge overlay or run 'nvoi firewall set default'")
	}

	return nil
}

func resolveTLSMode(edgeProxied bool, certPEM, keyPEM string) ingressTLSMode {
	if certPEM != "" || keyPEM != "" {
		return tlsProvided
	}
	if edgeProxied {
		return tlsEdgeOrigin
	}
	return tlsACME
}

func ingressDomains(routes []IngressRouteArg) []string {
	var domains []string
	for _, route := range routes {
		domains = append(domains, route.Domains...)
	}
	sort.Strings(domains)
	return domains
}
