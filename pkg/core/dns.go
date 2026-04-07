package core

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/getnvoi/nvoi/pkg/infra"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/provider/cloudflare"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// waitHTTPSFunc is the HTTPS reachability check. Variable for testing.
var waitHTTPSFunc = infra.WaitHTTPS
var nowFunc = time.Now
var createOriginCertFunc = func(ctx context.Context, apiKey string, domains []string) (*cloudflare.OriginCert, error) {
	return cloudflare.NewOriginCA(apiKey).CreateCert(ctx, domains)
}

// ── DNS ───────────────────────────────────────────────────────────────────────

type DNSSetRequest struct {
	Cluster
	DNS         ProviderRef
	Service     string
	Domains     []string
	EdgeProxied bool // edge overlay should proxy DNS through the provider
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

	if req.EdgeProxied && req.DNS.Name != "cloudflare" {
		return fmt.Errorf("edge-proxied DNS currently requires Cloudflare as DNS provider (current: %s)", req.DNS.Name)
	}

	for _, domain := range req.Domains {
		out.Progress(fmt.Sprintf("ensuring %s → %s", domain, ip))
		if err := dns.EnsureARecord(ctx, domain, ip, req.EdgeProxied); err != nil {
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

// DNSDelete removes DNS A records.
// Guarded: fails if ingress still references the service/domains.
func DNSDelete(ctx context.Context, req DNSDeleteRequest) error {
	out := req.Log()
	out.Command("dns", "delete", req.Service)

	dns, err := provider.ResolveDNS(req.DNS.Name, req.DNS.Creds)
	if err != nil {
		return err
	}

	if err := ensureDNSDeleteAllowed(ctx, req); err != nil {
		return err
	}

	for _, domain := range req.Domains {
		out.Progress(fmt.Sprintf("deleting %s", domain))
		if err := dns.DeleteARecord(ctx, domain); err != nil {
			return fmt.Errorf("dns delete %s: %w", domain, err)
		}
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

type resolvedIngressRuntime struct {
	exposure ingressExposureMode
	tls      ingressTLSMode
}

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

type IngressApplyRequest struct {
	Cluster
	DNS          ProviderRef // DNS provider — needed for explicit edge overlay helpers
	Routes       []IngressRouteArg
	TLSMode      string // acme, provided, edge_origin (optional; defaults from exposure if empty)
	EdgeProvider string // explicit edge overlay provider when applicable
	CertPEM      string // TLS cert PEM — custom cert instead of ACME (optional)
	KeyPEM       string // TLS key PEM (required if CertPEM is set)
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

	// Zero routes means ingress should be absent.
	if len(req.Routes) == 0 {
		out.Progress("removing caddy ingress")
		if err := clearIngress(ctx, ssh, ns, names); err != nil {
			return fmt.Errorf("clear ingress: %w", err)
		}
		out.Success("ingress removed")
		return nil
	}

	runtime, err := resolveIngressRuntime(req)
	if err != nil {
		return err
	}

	// Build routes from args — resolve each service's port from the cluster
	var routes []kube.IngressRoute
	for _, r := range req.Routes {
		port, err := kube.GetServicePort(ctx, ssh, ns, r.Service)
		if err != nil {
			return fmt.Errorf("service %q has no port — ingress requires a service with --port: %w", r.Service, err)
		}
		routes = append(routes, kube.IngressRoute{
			Service:      r.Service,
			Port:         port,
			Domains:      r.Domains,
			UseTLSSecret: runtime.tls != tlsACME,
		})
		out.Progress(fmt.Sprintf("%s → %s", r.Service, strings.Join(r.Domains, ", ")))
	}

	if err := checkFirewallCoherence(ctx, req.Cluster, runtime.exposure); err != nil {
		return err
	}

	certPEM, keyPEM, err := resolveTLSMaterial(ctx, req, runtime, out, ssh, ns)
	if err != nil {
		return err
	}

	// Store TLS cert if provided (custom cert or auto-generated Origin CA)
	hasCert := certPEM != "" && keyPEM != ""
	if hasCert {
		out.Progress("storing TLS certificate")
		if err := kube.UpsertTLSSecret(ctx, ssh, ns, kube.CaddyTLSSecretName, certPEM, keyPEM); err != nil {
			return fmt.Errorf("store cert: %w", err)
		}
		out.Success("certificate stored")
	} else {
		if _, err := kube.RunKubectl(ctx, ssh, ns, fmt.Sprintf("delete secret %s --ignore-not-found", kube.CaddyTLSSecretName)); err != nil {
			return fmt.Errorf("clear tls secret: %w", err)
		}
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

		if runtime.exposure == exposureEdgeProxied {
			out.Success(fmt.Sprintf("edge proxied — https://%s", firstDomain))
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

// ── Helpers ───────────────────────────────────────────────────────────────────

func ensureDNSDeleteAllowed(ctx context.Context, req DNSDeleteRequest) error {
	if req.Provider == "" {
		return nil
	}

	ssh, names, err := req.Cluster.SSH(ctx)
	if errors.Is(err, ErrNoMaster) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("dns delete guard: inspect ingress: %w", err)
	}
	defer ssh.Close()

	routes, err := kube.GetIngressRoutes(ctx, ssh, names.KubeNamespace(), names.KubeCaddyConfig())
	if err != nil {
		return fmt.Errorf("dns delete guard: inspect ingress: %w", err)
	}

	for _, route := range routes {
		if routeReferencesDomains(route, req.Domains) {
			return fmt.Errorf(
				"dns delete blocked: ingress still references %q (%s) — remove or reconcile ingress first",
				req.Service, strings.Join(req.Domains, ", "),
			)
		}
	}

	return nil
}

func routeReferencesDomains(route kube.IngressRoute, domains []string) bool {
	for _, want := range domains {
		for _, have := range route.Domains {
			if have == want {
				return true
			}
		}
	}
	return false
}

func clearIngress(ctx context.Context, ssh utils.SSHClient, ns string, names *utils.Names) error {
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

func resolveIngressRuntime(req IngressApplyRequest) (resolvedIngressRuntime, error) {
	exposure, err := resolveExposureMode(req.Routes)
	if err != nil {
		return resolvedIngressRuntime{}, err
	}

	certProvided := req.CertPEM != "" || req.KeyPEM != ""
	if certProvided && (req.CertPEM == "" || req.KeyPEM == "") {
		return resolvedIngressRuntime{}, fmt.Errorf("--cert and --key must both be provided")
	}

	switch req.TLSMode {
	case "":
		if certProvided {
			return resolvedIngressRuntime{exposure: exposure, tls: tlsProvided}, nil
		}
		if exposure == exposureEdgeProxied {
			return resolvedIngressRuntime{exposure: exposure, tls: tlsEdgeOrigin}, nil
		}
		return resolvedIngressRuntime{exposure: exposure, tls: tlsACME}, nil
	case string(tlsACME):
		if certProvided {
			return resolvedIngressRuntime{}, fmt.Errorf("tls mode %q does not accept provided cert/key material", tlsACME)
		}
		return resolvedIngressRuntime{exposure: exposure, tls: tlsACME}, nil
	case string(tlsProvided):
		if !certProvided {
			return resolvedIngressRuntime{}, fmt.Errorf("tls mode %q requires cert/key material", tlsProvided)
		}
		return resolvedIngressRuntime{exposure: exposure, tls: tlsProvided}, nil
	case string(tlsEdgeOrigin):
		if certProvided {
			return resolvedIngressRuntime{}, fmt.Errorf("tls mode %q does not accept provided cert/key material", tlsEdgeOrigin)
		}
		if exposure != exposureEdgeProxied {
			return resolvedIngressRuntime{}, fmt.Errorf("tls mode %q requires proxied edge exposure", tlsEdgeOrigin)
		}
		return resolvedIngressRuntime{exposure: exposure, tls: tlsEdgeOrigin}, nil
	default:
		return resolvedIngressRuntime{}, fmt.Errorf("unsupported ingress tls mode: %s", req.TLSMode)
	}
}

func resolveExposureMode(routes []IngressRouteArg) (ingressExposureMode, error) {
	anyEdge := false
	anyDirect := false
	for _, route := range routes {
		if route.EdgeProxied {
			anyEdge = true
		} else {
			anyDirect = true
		}
	}

	switch {
	case anyEdge && anyDirect:
		return "", fmt.Errorf("mixed direct and edge-proxied ingress routes are not supported — reconcile ingress to one exposure mode")
	case anyEdge:
		return exposureEdgeProxied, nil
	default:
		return exposureDirect, nil
	}
}

func resolveTLSMaterial(ctx context.Context, req IngressApplyRequest, runtime resolvedIngressRuntime, out Output, ssh utils.SSHClient, ns string) (string, string, error) {
	switch runtime.tls {
	case tlsACME:
		return "", "", nil
	case tlsProvided:
		return req.CertPEM, req.KeyPEM, nil
	case tlsEdgeOrigin:
		if req.EdgeProvider != "" && req.EdgeProvider != "cloudflare" {
			return "", "", fmt.Errorf("tls mode %q currently requires edge provider cloudflare (current: %s)", tlsEdgeOrigin, req.EdgeProvider)
		}
		if req.DNS.Name != "cloudflare" {
			return "", "", fmt.Errorf("tls mode %q currently requires Cloudflare as DNS provider (current: %s)", tlsEdgeOrigin, req.DNS.Name)
		}
		// The deploy-owned artifact is the in-cluster TLS secret. We reuse,
		// replace, and remove that artifact deterministically here. Provider-side
		// Origin CA cert retention is not currently revoked by nvoi and may remain
		// retained remotely after replacement/removal.
		domains := ingressDomains(req.Routes)
		if certPEM, keyPEM, ok := loadReusableOriginCert(ctx, ssh, ns, domains); ok {
			out.Success("origin cert reused")
			return certPEM, keyPEM, nil
		}
		out.Progress("generating Cloudflare Origin CA certificate")
		originCert, err := createOriginCertFunc(ctx, req.DNS.Creds["api_key"], domains)
		if err != nil {
			return "", "", fmt.Errorf("origin ca cert: %w", err)
		}
		out.Success("origin cert ready")
		return originCert.Certificate, originCert.PrivateKey, nil
	default:
		return "", "", fmt.Errorf("unsupported ingress tls mode: %s", runtime.tls)
	}
}

func ingressDomains(routes []IngressRouteArg) []string {
	var domains []string
	for _, route := range routes {
		domains = append(domains, route.Domains...)
	}
	sort.Strings(domains)
	return domains
}

func loadReusableOriginCert(ctx context.Context, ssh utils.SSHClient, ns string, domains []string) (string, string, bool) {
	certPEM, err := kube.GetSecretValue(ctx, ssh, ns, kube.CaddyTLSSecretName, "tls.crt")
	if err != nil {
		return "", "", false
	}
	keyPEM, err := kube.GetSecretValue(ctx, ssh, ns, kube.CaddyTLSSecretName, "tls.key")
	if err != nil {
		return "", "", false
	}
	if !originCertMatchesDomains(certPEM, domains) {
		return "", "", false
	}
	return certPEM, keyPEM, true
}

func originCertMatchesDomains(certPEM string, domains []string) bool {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}
	if !cert.NotAfter.After(nowFunc()) {
		return false
	}

	certDomains := append([]string(nil), cert.DNSNames...)
	if len(certDomains) == 0 && cert.Subject.CommonName != "" {
		certDomains = append(certDomains, cert.Subject.CommonName)
	}
	sort.Strings(certDomains)

	expected := append([]string(nil), domains...)
	sort.Strings(expected)
	if len(certDomains) != len(expected) {
		return false
	}
	for i := range certDomains {
		if certDomains[i] != expected[i] {
			return false
		}
	}
	return true
}
