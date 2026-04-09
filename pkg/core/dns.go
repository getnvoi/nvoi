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

	for _, domain := range req.Domains {
		out.Progress(fmt.Sprintf("ensuring %s → %s", domain, ip))
		if err := dns.EnsureARecord(ctx, domain, ip, false); err != nil {
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
