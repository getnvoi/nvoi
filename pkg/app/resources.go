package app

import (
	"context"

	"github.com/getnvoi/nvoi/pkg/provider"
)

type ResourcesRequest struct {
	Compute ProviderRef
	DNS     ProviderRef
	Storage ProviderRef
}

func Resources(ctx context.Context, req ResourcesRequest) ([]provider.ResourceGroup, error) {
	var all []provider.ResourceGroup

	// Compute resources
	if req.Compute.Name != "" {
		prov, err := provider.ResolveCompute(req.Compute.Name, req.Compute.Creds)
		if err != nil {
			return nil, err
		}
		groups, err := prov.ListResources(ctx)
		if err != nil {
			return nil, err
		}
		all = append(all, groups...)
	}

	// DNS resources
	if req.DNS.Name != "" {
		dns, err := provider.ResolveDNS(req.DNS.Name, req.DNS.Creds)
		if err == nil {
			groups, err := dns.ListResources(ctx)
			if err == nil {
				all = append(all, groups...)
			}
		}
	}

	// Storage resources
	if req.Storage.Name != "" {
		bucket, err := provider.ResolveBucket(req.Storage.Name, req.Storage.Creds)
		if err == nil {
			groups, err := bucket.ListResources(ctx)
			if err == nil {
				all = append(all, groups...)
			}
		}
	}

	return all, nil
}
