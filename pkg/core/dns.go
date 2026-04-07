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

// DNSDelete removes DNS A records and the corresponding Caddy route.
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

	// Remove route from Caddy if we have cluster access
	if req.Provider != "" {
		ssh, names, err := req.Cluster.SSH(ctx)
		if errors.Is(err, ErrNoMaster) {
			return nil
		}
		if err != nil {
			return nil
		}
		defer ssh.Close()

		ns := names.KubeNamespace()
		existing, _ := kube.GetIngressRoutes(ctx, ssh, ns, names.KubeCaddyConfig())
		routes := removeRoute(existing, req.Service)

		if len(routes) == 0 {
			kube.DeleteByName(ctx, ssh, ns, names.KubeCaddy())
			kube.RunKubectl(ctx, ssh, ns, fmt.Sprintf("delete configmap %s --ignore-not-found", names.KubeCaddyConfig()))
		} else {
			kube.ApplyCaddyConfig(ctx, ssh, ns, routes, names)
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

// IngressRoute is a parsed service:domain,domain arg for ingress apply.
type IngressRouteArg struct {
	Service string
	Domains []string
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
	Routes []IngressRouteArg
}

// IngressApply builds the full Caddyfile from the given routes and deploys Caddy once.
func IngressApply(ctx context.Context, req IngressApplyRequest) error {
	out := req.Log()
	master, names, _, err := req.Cluster.Master(ctx)
	if err != nil {
		return err
	}

	ssh, err := infra.ConnectSSH(ctx, master.IPv4+":22", utils.DefaultUser, req.SSHKey)
	if err != nil {
		return fmt.Errorf("ssh master: %w", err)
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
		})
		out.Progress(fmt.Sprintf("%s → %s", r.Service, strings.Join(r.Domains, ", ")))
	}

	if len(routes) == 0 {
		out.Info("no routes — skipping caddy")
		return nil
	}

	out.Progress("applying caddy config")
	if err := kube.ApplyCaddyConfig(ctx, ssh, ns, routes, names); err != nil {
		return fmt.Errorf("caddy: %w", err)
	}
	out.Success("caddy ready")

	// Wait for HTTPS on the first domain
	firstDomain := req.Routes[0].Domains[0]
	out.Progress(fmt.Sprintf("waiting for https://%s", firstDomain))
	if err := infra.WaitHTTPS(ctx, firstDomain); err != nil {
		out.Warning("domain not responding yet (TLS may still be provisioning)")
	} else {
		out.Success(fmt.Sprintf("https://%s live", firstDomain))
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
