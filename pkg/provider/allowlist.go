package provider

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/getnvoi/nvoi/pkg/utils"
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

// ResolveFirewallArgs parses CLI args into a PortAllowList.
// First arg may be a preset name ("default", "cloudflare").
// Remaining args are raw "port:cidr,cidr" rules that override the preset.
// SSH (22) is never included — it's always open, managed by instance set.
func ResolveFirewallArgs(ctx context.Context, args []string) (PortAllowList, error) {
	if len(args) == 0 {
		return nil, nil
	}

	var base PortAllowList
	rawArgs := args

	// Check if first arg is a preset name (no ":" = not a raw rule)
	if len(args) > 0 && !strings.Contains(args[0], ":") {
		preset, err := resolvePreset(ctx, args[0])
		if err != nil {
			return nil, err
		}
		base = preset
		rawArgs = args[1:]
	}

	// Parse raw rules
	overrides := ParseRawRules(rawArgs)

	// Merge: raw overrides win for same port
	return MergeAllowLists(base, overrides), nil
}

// resolvePreset returns the PortAllowList for a named preset.
// SSH (22) is NOT included — always open, managed separately by instance set.
func resolvePreset(ctx context.Context, name string) (PortAllowList, error) {
	switch name {
	case "default":
		return PortAllowList{
			"80":  {"0.0.0.0/0", "::/0"},
			"443": {"0.0.0.0/0", "::/0"},
		}, nil

	case "cloudflare":
		cidrs, err := FetchCloudflareIPs(ctx)
		if err != nil {
			cidrs = FallbackCloudflareIPs
		}
		return PortAllowList{
			"80":  cidrs,
			"443": cidrs,
		}, nil

	default:
		return nil, fmt.Errorf("unknown firewall preset: %q (available: default, cloudflare)", name)
	}
}

// FetchCloudflareIPs fetches Cloudflare's published IP ranges.
// GET https://api.cloudflare.com/client/v4/ips → {"result": {"ipv4_cidrs": [...]}}
func FetchCloudflareIPs(ctx context.Context) ([]string, error) {
	client := &utils.HTTPClient{
		BaseURL: "https://api.cloudflare.com/client/v4",
		Label:   "cloudflare ips",
	}
	var body struct {
		Result struct {
			IPv4CIDRs []string `json:"ipv4_cidrs"`
		} `json:"result"`
	}
	if err := client.Do(ctx, "GET", "/ips", nil, &body); err != nil {
		return nil, fmt.Errorf("fetch cloudflare IPs: %w", err)
	}
	if len(body.Result.IPv4CIDRs) == 0 {
		return nil, fmt.Errorf("cloudflare IPs API returned empty list")
	}
	return body.Result.IPv4CIDRs, nil
}

// FallbackCloudflareIPs is used when the API fetch fails (offline deploys).
var FallbackCloudflareIPs = []string{
	"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22",
	"103.31.4.0/22", "141.101.64.0/18", "108.162.192.0/18",
	"190.93.240.0/20", "188.114.96.0/20", "197.234.240.0/22",
	"198.41.128.0/17", "162.158.0.0/15", "104.16.0.0/13",
	"104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
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
