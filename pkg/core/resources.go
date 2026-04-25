package core

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/pkg/provider"
)

type ResourcesRequest struct {
	Infra   ProviderRef
	DNS     ProviderRef
	Storage ProviderRef
	Tunnel  ProviderRef
}

func Resources(ctx context.Context, req ResourcesRequest) ([]provider.ResourceGroup, error) {
	var all []provider.ResourceGroup

	// Infra resources (servers, firewalls, volumes, networks)
	if req.Infra.Name != "" {
		prov, err := provider.ResolveInfra(req.Infra.Name, req.Infra.Creds)
		if err != nil {
			return nil, fmt.Errorf("infra %q: %w", req.Infra.Name, err)
		}
		defer func() { _ = prov.Close() }()
		groups, err := prov.ListResources(ctx)
		if err != nil {
			return nil, fmt.Errorf("infra %q list: %w", req.Infra.Name, err)
		}
		all = append(all, groups...)
	}

	// DNS resources
	if req.DNS.Name != "" {
		dns, err := provider.ResolveDNS(req.DNS.Name, req.DNS.Creds)
		if err != nil {
			return nil, fmt.Errorf("dns %q: %w", req.DNS.Name, err)
		}
		groups, err := dns.ListResources(ctx)
		if err != nil {
			return nil, fmt.Errorf("dns %q list: %w", req.DNS.Name, err)
		}
		all = append(all, groups...)
	}

	// Storage resources
	if req.Storage.Name != "" {
		bucket, err := provider.ResolveBucket(req.Storage.Name, req.Storage.Creds)
		if err != nil {
			return nil, fmt.Errorf("storage %q: %w", req.Storage.Name, err)
		}
		groups, err := bucket.ListResources(ctx)
		if err != nil {
			return nil, fmt.Errorf("storage %q list: %w", req.Storage.Name, err)
		}
		all = append(all, groups...)
	}

	// Tunnel resources
	if req.Tunnel.Name != "" {
		tun, err := provider.ResolveTunnel(req.Tunnel.Name, req.Tunnel.Creds)
		if err != nil {
			return nil, fmt.Errorf("tunnel %q: %w", req.Tunnel.Name, err)
		}
		groups, err := tun.ListResources(ctx)
		if err != nil {
			return nil, fmt.Errorf("tunnel %q list: %w", req.Tunnel.Name, err)
		}
		all = append(all, groups...)
	}

	return all, nil
}
