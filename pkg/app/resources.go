package app

import (
	"context"

	"github.com/getnvoi/nvoi/pkg/provider"
)

type ResourcesRequest struct {
	Compute ProviderRef
	DNS     ProviderRef
}

type ResourcesResult struct {
	Servers    []*provider.Server
	Firewalls  []*provider.Firewall
	Networks   []*provider.Network
	DNSRecords []provider.DNSRecord
}

func Resources(ctx context.Context, req ResourcesRequest) (*ResourcesResult, error) {
	prov, err := provider.ResolveCompute(req.Compute.Name, req.Compute.Creds)
	if err != nil {
		return nil, err
	}

	servers, err := prov.ListServers(ctx, nil)
	if err != nil {
		return nil, err
	}
	firewalls, err := prov.ListAllFirewalls(ctx)
	if err != nil {
		return nil, err
	}
	networks, err := prov.ListAllNetworks(ctx)
	if err != nil {
		return nil, err
	}

	result := &ResourcesResult{
		Servers:   servers,
		Firewalls: firewalls,
		Networks:  networks,
	}

	// DNS records (optional)
	if req.DNS.Name != "" {
		dns, err := provider.ResolveDNS(req.DNS.Name, req.DNS.Creds)
		if err == nil {
			records, err := dns.ListARecords(ctx)
			if err == nil {
				result.DNSRecords = records
			}
		}
	}

	return result, nil
}

// ResourcesJSON returns resources keyed by provider name.
func ResourcesJSON(ctx context.Context, req ResourcesRequest) (map[string]any, error) {
	res, err := Resources(ctx, req)
	if err != nil {
		return nil, err
	}

	out := map[string]any{}
	if req.Compute.Name != "" {
		out[req.Compute.Name] = map[string]any{
			"servers":   res.Servers,
			"firewalls": res.Firewalls,
			"networks":  res.Networks,
		}
	}
	if req.DNS.Name != "" && len(res.DNSRecords) > 0 {
		out[req.DNS.Name] = map[string]any{
			"dns_records": res.DNSRecords,
		}
	}
	return out, nil
}
