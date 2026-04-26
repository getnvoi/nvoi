package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/pkg/provider"
)

type ResourcesRequest struct {
	Infra   ProviderRef
	DNS     ProviderRef
	Storage ProviderRef
	Tunnel  ProviderRef

	// Owner is the cfg-derived OwnershipContext used to classify each
	// returned row's Ownership column. Built once by cmd/cli from the
	// loaded `nvoi.yaml`. nil → classifier treats every nvoi-shaped
	// row as OwnershipOther (no cfg loaded).
	Owner *provider.OwnershipContext
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

	Classify(all, req.Owner)
	return all, nil
}

// Classify stamps the binary Scope on every row of every group, in
// place. Single source of truth — provider package never imports
// anything scope-related; ListResources implementations just emit
// rows.
//
// The mapping from group.Name → expected-set is the only place that
// knows which provider produces what. Adding a new resource kind =
// adding a case here.
func Classify(groups []provider.ResourceGroup, ctx *provider.OwnershipContext) {
	for i := range groups {
		expected, ok := expectedSetFor(groups[i].Name, ctx)
		if !ok {
			continue // unknown group — no Scope column rendered
		}
		nameIdx := nameColumnIndex(groups[i].Columns)
		if nameIdx < 0 {
			continue // no obvious name column to read
		}
		groups[i].Scope = make([]provider.Scope, len(groups[i].Rows))
		for j, row := range groups[i].Rows {
			name := ""
			if nameIdx < len(row) {
				name = row[nameIdx]
			}
			groups[i].Scope[j] = provider.ClassifyScope(name, expected)
		}
	}
}

// expectedSetFor picks the cfg's expected-name set for a group by its
// declared Name. Returns ok=false for groups outside our taxonomy
// (Subnets, Route Tables, github-actions-secrets) — those render
// without a Scope column.
func expectedSetFor(groupName string, ctx *provider.OwnershipContext) (map[string]bool, bool) {
	var set map[string]bool
	switch groupName {
	case "Servers", "Instances":
		if ctx != nil {
			set = ctx.ExpectedServers
		}
	case "Firewalls", "Security Groups":
		if ctx != nil {
			set = ctx.ExpectedFirewalls
		}
	case "Networks", "VPCs", "Private Networks":
		if ctx != nil {
			set = ctx.ExpectedNetworks
		}
	case "Volumes", "EBS Volumes", "Block Volumes":
		if ctx != nil {
			set = ctx.ExpectedVolumes
		}
	case "DNS Records":
		if ctx != nil {
			set = ctx.ExpectedDNS
		}
	case "R2 Buckets", "S3 Buckets", "Scaleway Buckets":
		if ctx != nil {
			set = ctx.ExpectedBuckets
		}
	case "Cloudflare Tunnels", "ngrok Reserved Domains":
		if ctx != nil {
			set = ctx.ExpectedTunnels
		}
	default:
		return nil, false
	}
	return set, true
}

// nameColumnIndex finds the canonical name column by header. Most
// groups call it "Name"; DNS Records use "Domain". Returns -1 if no
// recognized column is present.
func nameColumnIndex(cols []string) int {
	for i, c := range cols {
		if strings.EqualFold(c, "Name") || strings.EqualFold(c, "Domain") {
			return i
		}
	}
	return -1
}
