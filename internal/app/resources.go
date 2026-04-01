package app

import (
	"context"

	"github.com/getnvoi/nvoi/internal/provider"
)

type ResourcesRequest struct {
	Provider    string
	Credentials map[string]string
}

type ResourcesResult struct {
	Servers   []*provider.Server
	Firewalls []*provider.Firewall
	Networks  []*provider.Network
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

	return &ResourcesResult{
		Servers:   servers,
		Firewalls: firewalls,
		Networks:  networks,
	}, nil
}
