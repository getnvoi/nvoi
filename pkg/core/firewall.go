package core

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/pkg/provider"
)

type FirewallSetRequest struct {
	Cluster
	AllowedIPs provider.PortAllowList
}

func FirewallSet(ctx context.Context, req FirewallSetRequest) error {
	out := req.Log()
	names, err := req.Names()
	if err != nil {
		return err
	}
	prov, err := req.Compute()
	if err != nil {
		return err
	}

	out.Command("firewall", "set", names.Firewall())

	if err := prov.ReconcileFirewallRules(ctx, names.Firewall(), req.AllowedIPs); err != nil {
		return fmt.Errorf("firewall set: %w", err)
	}

	if len(req.AllowedIPs) == 0 {
		out.Success("base rules only (SSH + internal)")
	} else {
		for _, port := range provider.SortedPorts(req.AllowedIPs) {
			out.Success(fmt.Sprintf("port %s → %v", port, req.AllowedIPs[port]))
		}
	}
	return nil
}

type FirewallDeleteRequest struct {
	Cluster
}

func FirewallDelete(ctx context.Context, req FirewallDeleteRequest) error {
	out := req.Log()
	names, err := req.Names()
	if err != nil {
		return err
	}
	prov, err := req.Compute()
	if err != nil {
		return err
	}

	name := names.Firewall()
	out.Command("firewall", "delete", name)

	firewalls, _ := prov.ListAllFirewalls(ctx)
	found := false
	for _, fw := range firewalls {
		if fw.Name == name {
			found = true
			break
		}
	}

	if !found {
		out.Success(name + " already deleted")
		return nil
	}

	if err := prov.DeleteFirewall(ctx, name); err != nil {
		return fmt.Errorf("firewall delete: %w", err)
	}
	out.Success(name + " deleted")
	return nil
}

type NetworkDeleteRequest struct {
	Cluster
}

func NetworkDelete(ctx context.Context, req NetworkDeleteRequest) error {
	out := req.Log()
	names, err := req.Names()
	if err != nil {
		return err
	}
	prov, err := req.Compute()
	if err != nil {
		return err
	}

	name := names.Network()
	out.Command("network", "delete", name)

	networks, _ := prov.ListAllNetworks(ctx)
	found := false
	for _, n := range networks {
		if n.Name == name {
			found = true
			break
		}
	}

	if !found {
		out.Success(name + " already deleted")
		return nil
	}

	if err := prov.DeleteNetwork(ctx, name); err != nil {
		return fmt.Errorf("network delete: %w", err)
	}
	out.Success(name + " deleted")
	return nil
}

type FirewallListRequest struct {
	Cluster
}

func FirewallList(ctx context.Context, req FirewallListRequest) (provider.PortAllowList, error) {
	names, err := req.Names()
	if err != nil {
		return nil, err
	}
	prov, err := req.Compute()
	if err != nil {
		return nil, err
	}
	return prov.GetFirewallRules(ctx, names.Firewall())
}
