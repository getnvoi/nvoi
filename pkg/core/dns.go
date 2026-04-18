package core

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/pkg/provider"
)

// ── DNS ───────────────────────────────────────────────────────────────────────

type DNSSetRequest struct {
	Cluster
	DNS     ProviderRef
	Service string
	Domains []string
}

// DNSSet routes domains to the master node via the configured DNS
// provider. The IngressBinding is constructed inline (DNSType:"A",
// DNSTarget:master.IPv4) — once C6 lands, the reconciler delegates to
// infra.IngressBinding(svc) so managed-k8s providers can return CNAMEs
// and we never assume the binding shape here.
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

	binding := provider.IngressBinding{DNSType: "A", DNSTarget: ip}
	for _, domain := range req.Domains {
		out.Progress(fmt.Sprintf("ensuring %s → %s", domain, ip))
		if err := dns.RouteTo(ctx, domain, binding); err != nil {
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

// DNSDelete removes DNS records for the given domains. Idempotent —
// providers' Unroute returns nil for missing records (matches the old
// "already deleted" UX without per-error inspection).
func DNSDelete(ctx context.Context, req DNSDeleteRequest) error {
	out := req.Log()
	out.Command("dns", "delete", req.Service)

	dns, err := provider.ResolveDNS(req.DNS.Name, req.DNS.Creds)
	if err != nil {
		return err
	}

	for _, domain := range req.Domains {
		if err := dns.Unroute(ctx, domain); err != nil {
			return fmt.Errorf("dns delete %s: %w", domain, err)
		}
		out.Success(fmt.Sprintf("%s deleted", domain))
	}

	return nil
}

type DNSListRequest struct {
	DNS    ProviderRef
	Output Output
}

func DNSList(ctx context.Context, req DNSListRequest) ([]provider.DNSBinding, error) {
	dns, err := provider.ResolveDNS(req.DNS.Name, req.DNS.Creds)
	if err != nil {
		return nil, err
	}
	return dns.ListBindings(ctx)
}
