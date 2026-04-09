package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
)

// ── DNS ───────────────────────────────────────────────────────────────────────

type DNSSetRequest struct {
	Cluster
	DNS               ProviderRef
	Service           string
	Domains           []string
	CloudflareManaged bool
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

	if req.CloudflareManaged && req.DNS.Name != "cloudflare" {
		return fmt.Errorf("cloudflare-managed DNS requires Cloudflare as DNS provider (current: %s)", req.DNS.Name)
	}

	for _, domain := range req.Domains {
		out.Progress(fmt.Sprintf("ensuring %s → %s", domain, ip))
		if err := dns.EnsureARecord(ctx, domain, ip, req.CloudflareManaged); err != nil {
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
