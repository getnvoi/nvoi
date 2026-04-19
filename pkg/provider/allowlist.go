// Package provider defines interfaces and registration for compute, DNS, storage, and build providers.
package provider

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// InternalPorts are always managed by instance set — never user-configurable.
// k8s API (6443), kubelet (10250), flannel VXLAN (8472), registry (5000).
var InternalPorts = map[string]bool{
	"6443":  true,
	"10250": true,
	"8472":  true,
	"5000":  true,
}

// IsInternalPort returns true for ports that are always managed internally.
func IsInternalPort(port string) bool {
	return InternalPorts[port]
}

// SortedPorts returns the port keys of a PortAllowList in numerically sorted order.
func SortedPorts(m PortAllowList) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, _ := strconv.Atoi(keys[i])
		b, _ := strconv.Atoi(keys[j])
		return a < b
	})
	return keys
}

// PortAllowList maps port strings to allowed source CIDRs.
// Ports present override defaults. Ports absent are closed (for public ports)
// or keep provider defaults (for internal ports).
// A nil map = base rules only (SSH open + internal).
type PortAllowList map[string][]string

// ResolveFirewallArgs parses firewall rule strings into a PortAllowList.
// All entries must be in "port:cidr" or "port:cidr,cidr" format.
// A plain word with no ":" (former preset names) is now a hard error —
// presets were removed; 80/443 are auto-derived from config state instead.
// SSH (22) is never included — always open, managed by instance set.
func ResolveFirewallArgs(_ context.Context, args []string) (PortAllowList, error) {
	for _, arg := range args {
		trimmed := strings.TrimSpace(arg)
		if trimmed != "" && !strings.Contains(trimmed, ":") {
			return nil, fmt.Errorf("firewall: %q is not a valid rule — use port:cidr format (e.g. 22:1.2.3.4/32); presets have been removed", trimmed)
		}
	}
	return ParseRawRules(args), nil
}

// FirewallAllowList derives the master-firewall allow-list from the config.
//
// Rule: 80/443 are auto-opened when domains are declared AND no tunnel
// provider is configured (Caddy mode). In tunnel mode or with no domains
// the allow-list base is nil — master gets SSH + internal ports only.
//
// User overrides from cfg.FirewallRules() are merged on top of the derived
// base (same port in overrides wins). The validator already rejects 80/443
// overrides in tunnel mode, so the merge here never re-opens closed ports.
func FirewallAllowList(ctx context.Context, cfg ProviderConfigView) (PortAllowList, error) {
	base := PortAllowList(nil)
	if len(cfg.DomainsByService()) > 0 && cfg.TunnelProvider() == "" {
		// Caddy mode: master needs public HTTP(S) to serve ACME + traffic.
		base = PortAllowList{
			"80":  {"0.0.0.0/0", "::/0"},
			"443": {"0.0.0.0/0", "::/0"},
		}
	}
	userRules, err := ResolveFirewallArgs(ctx, cfg.FirewallRules())
	if err != nil {
		return nil, err
	}
	return MergeAllowLists(base, userRules), nil
}

// MergeAllowLists merges base + overrides. Override wins for same port.
func MergeAllowLists(base, overrides PortAllowList) PortAllowList {
	if base == nil && overrides == nil {
		return nil
	}
	result := PortAllowList{}
	for port, ips := range base {
		result[port] = ips
	}
	for port, ips := range overrides {
		result[port] = ips // override wins
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// ParseRawRules parses "port:cidr,cidr" args (no preset handling).
// Also handles semicolon-separated format from env vars.
func ParseRawRules(args []string) PortAllowList {
	result := PortAllowList{}
	for _, arg := range args {
		for _, group := range strings.Split(arg, ";") {
			group = strings.TrimSpace(group)
			port, cidrs, ok := strings.Cut(group, ":")
			if !ok || port == "" {
				continue
			}
			portNum, err := strconv.Atoi(port)
			if err != nil || portNum < 1 || portNum > 65535 {
				continue // skip invalid ports
			}
			for _, cidr := range strings.Split(cidrs, ",") {
				cidr = strings.TrimSpace(cidr)
				if cidr == "" {
					continue
				}
				if !strings.Contains(cidr, "/") {
					cidr += "/32"
				}
				result[port] = append(result[port], cidr)
			}
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}
