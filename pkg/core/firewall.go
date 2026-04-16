package core

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/pkg/provider"
)

type FirewallSetRequest struct {
	Cluster
	Output     Output
	Name       string // firewall resource name (e.g. names.MasterFirewall())
	AllowedIPs provider.PortAllowList
}

func FirewallSet(ctx context.Context, req FirewallSetRequest) error {
	out := log(req.Output)
	prov, err := req.Compute()
	if err != nil {
		return err
	}

	out.Command("firewall", "set", req.Name)

	if err := prov.ReconcileFirewallRules(ctx, req.Name, req.AllowedIPs); err != nil {
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
	Output Output
	Name   string // firewall resource name
}

func FirewallDelete(ctx context.Context, req FirewallDeleteRequest) error {
	out := log(req.Output)
	prov, err := req.Compute()
	if err != nil {
		return err
	}

	out.Command("firewall", "delete", req.Name)

	firewalls, _ := prov.ListAllFirewalls(ctx)
	found := false
	for _, fw := range firewalls {
		if fw.Name == req.Name {
			found = true
			break
		}
	}

	if !found {
		out.Success(req.Name + " already deleted")
		return nil
	}

	if err := prov.DeleteFirewall(ctx, req.Name); err != nil {
		return fmt.Errorf("firewall delete: %w", err)
	}
	out.Success(req.Name + " deleted")
	return nil
}

type NetworkDeleteRequest struct {
	Cluster
	Output Output
}

func NetworkDelete(ctx context.Context, req NetworkDeleteRequest) error {
	out := log(req.Output)
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

type FirewallListAllRequest struct {
	Cluster
	Output Output
}

func FirewallListAll(ctx context.Context, req FirewallListAllRequest) ([]*provider.Firewall, error) {
	prov, err := req.Compute()
	if err != nil {
		return nil, err
	}
	return prov.ListAllFirewalls(ctx)
}

// FirewallRemoveOrphans deletes all firewalls matching the cluster prefix
// that are NOT in the desired set. Reconcile passes desired = {master, worker}.
// Teardown passes desired = nil (delete everything).
type FirewallRemoveOrphansRequest struct {
	Cluster
	Output  Output
	Prefix  string          // names.Base() + "-" — only touch our firewalls
	Desired map[string]bool // firewalls to keep (nil = delete all)
}

func FirewallRemoveOrphans(ctx context.Context, req FirewallRemoveOrphansRequest) []error {
	out := log(req.Output)
	all, err := FirewallListAll(ctx, FirewallListAllRequest{Cluster: req.Cluster, Output: req.Output})
	if err != nil {
		return []error{err}
	}

	var errs []error
	for _, fw := range all {
		if len(fw.Name) <= len(req.Prefix) || fw.Name[:len(req.Prefix)] != req.Prefix {
			continue // not ours
		}
		if req.Desired[fw.Name] {
			continue // desired, keep
		}
		if err := FirewallDelete(ctx, FirewallDeleteRequest{
			Cluster: req.Cluster, Output: req.Output, Name: fw.Name,
		}); err != nil {
			out.Warning(fmt.Sprintf("orphan firewall %s not removed: %s", fw.Name, err))
			errs = append(errs, err)
		}
	}
	return errs
}
