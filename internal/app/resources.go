package app

import (
	"context"

	"github.com/getnvoi/nvoi/internal/provider"
)

type ResourcesRequest struct {
	Provider    string
	Credentials map[string]string
	DNSProvider string
	DNSCreds    map[string]string
}

type ResourcesResult struct {
	Servers    []*provider.Server
	Firewalls  []*provider.Firewall
	Networks   []*provider.Network
	DNSRecords []provider.DNSRecord
}

func Resources(ctx context.Context, req ResourcesRequest) (*ResourcesResult, error) {
	prov, err := provider.ResolveCompute(req.Provider, req.Credentials)
	if err != nil {
		return nil, err
	}

	servers, err := prov.ListAllServers(ctx)
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
	if req.DNSProvider != "" {
		dns, err := provider.ResolveDNS(req.DNSProvider, req.DNSCreds)
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
	if req.Provider != "" {
		out[req.Provider] = map[string]any{
			"servers":   res.Servers,
			"firewalls": res.Firewalls,
			"networks":  res.Networks,
		}
	}
	if req.DNSProvider != "" && len(res.DNSRecords) > 0 {
		out[req.DNSProvider] = map[string]any{
			"dns_records": res.DNSRecords,
		}
	}
	return out, nil
}
