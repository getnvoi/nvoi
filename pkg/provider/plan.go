package provider

import (
	"context"
	"fmt"
	"sort"

	"github.com/getnvoi/nvoi/pkg/utils"
)

// Plan resource constants — every PlanEntry's Resource field is one of these.
// Constants live here so producers (provider impls, reconcile step planners)
// and consumers (renderer, prompt logic) agree on the same string set.
const (
	ResServer         = "server"
	ResFirewall       = "firewall"
	ResFirewallRule   = "firewall-rule"
	ResVolume         = "volume"
	ResNetwork        = "network"
	ResDNS            = "dns"
	ResBucket         = "bucket"
	ResDatabase       = "database"
	ResTunnel         = "tunnel"
	ResNamespace      = "namespace"
	ResRegistrySecret = "registry-secret"
	ResWorkload       = "workload"
	ResSecretKey      = "secret-key"
	ResCronJob        = "cronjob"
	ResCaddyRoute     = "caddy-route"
)

// PlanKind labels each entry as add / delete / update. The string value is
// stable for JSON output; renderers map to glyphs via Glyph().
type PlanKind string

const (
	PlanAdd      PlanKind = "add"
	PlanDelete   PlanKind = "delete"
	PlanUpdate   PlanKind = "update"
	PlanNoChange PlanKind = "unchanged"
)

// Glyph returns the renderer symbol for this kind (+ / - / ~).
// `~` doubles for change AND unchanged — the Word column disambiguates
// in the standalone plan inventory; the deploy preamble filters out
// PlanNoChange so the conflation never matters there.
func (k PlanKind) Glyph() string {
	switch k {
	case PlanAdd:
		return "+"
	case PlanDelete:
		return "-"
	case PlanUpdate, PlanNoChange:
		return "~"
	default:
		return "?"
	}
}

// Word returns the human-readable status word for the inventory
// table's STATUS column ("add" / "change" / "unchanged" / "remove").
func (k PlanKind) Word() string {
	switch k {
	case PlanAdd:
		return "add"
	case PlanDelete:
		return "remove"
	case PlanUpdate:
		return "change"
	case PlanNoChange:
		return "unchanged"
	default:
		return string(k)
	}
}

// PlanEntry is one diff-line in the plan output. Reason is the
// auto-skip flag: when non-empty, this entry does NOT contribute to the
// confirmation prompt — it'll be applied silently. Today the only
// auto-skip reason is "image-tag" (a service whose only delta is the
// per-deploy hash in its image tag, which churns every deploy).
type PlanEntry struct {
	Kind     PlanKind
	Resource string // one of the Res* constants
	Name     string // resource short name (e.g. "master", "master-fw:80")
	Detail   string // human-readable annotation; renderer uses verbatim
	Reason   string // non-empty → auto-skip from prompt
}

// Promptable returns true when this entry requires confirmation.
func (e PlanEntry) Promptable() bool { return e.Reason == "" }

// ComputeInfraPlan diffs the desired infra state (cfg) against the live
// state (snap) and returns plan entries for servers / firewalls
// (existence + per-port rules) / volumes. Provider-agnostic: each
// InfraProvider's PlanInfra produces its own LiveSnapshot then calls
// this. snap == nil signals first deploy — every desired resource
// emerges as PlanAdd.
//
// Network is intentionally NOT diffed here: LiveSnapshot doesn't carry
// network state today, and providers create the network implicitly
// during server provisioning. Add when LiveSnapshot grows a Network
// field.
func ComputeInfraPlan(ctx context.Context, cfg ProviderConfigView, snap *LiveSnapshot, names *utils.Names) ([]PlanEntry, error) {
	var entries []PlanEntry

	// --- Servers ---
	desiredServers := map[string]bool{}
	for _, s := range cfg.ServerDefs() {
		desiredServers[s.Name] = true
	}
	liveServers := map[string]bool{}
	if snap != nil {
		for _, s := range snap.Servers {
			liveServers[s] = true
		}
	}
	// Iterate cfg.ServerDefs() in declared order for stable output.
	for _, s := range cfg.ServerDefs() {
		detail := fmt.Sprintf("%s %s", s.Type, s.Region)
		if !liveServers[s.Name] {
			entries = append(entries, PlanEntry{
				Kind: PlanAdd, Resource: ResServer, Name: s.Name, Detail: detail,
			})
			continue
		}
		entries = append(entries, PlanEntry{
			Kind: PlanNoChange, Resource: ResServer, Name: s.Name, Detail: detail,
		})
	}
	if snap != nil {
		sortedLive := append([]string(nil), snap.Servers...)
		sort.Strings(sortedLive)
		for _, name := range sortedLive {
			if desiredServers[name] {
				continue
			}
			entries = append(entries, PlanEntry{
				Kind: PlanDelete, Resource: ResServer, Name: name,
			})
		}
	}

	// --- Volumes (user-declared + per-builder cache volumes) ---
	desiredVols := map[string]bool{}
	for _, v := range cfg.VolumeDefs() {
		desiredVols[v.Name] = true
	}
	for _, s := range cfg.ServerDefs() {
		if s.Role == utils.RoleBuilder {
			desiredVols[names.BuilderCacheVolumeShort(s.Name)] = true
		}
	}
	liveVols := map[string]bool{}
	if snap != nil {
		for _, v := range snap.Volumes {
			liveVols[v] = true
		}
	}
	for _, v := range cfg.VolumeDefs() {
		detail := fmt.Sprintf("%dGB on %s", v.Size, v.Server)
		if !liveVols[v.Name] {
			entries = append(entries, PlanEntry{
				Kind: PlanAdd, Resource: ResVolume, Name: v.Name, Detail: detail,
			})
			continue
		}
		entries = append(entries, PlanEntry{
			Kind: PlanNoChange, Resource: ResVolume, Name: v.Name, Detail: detail,
		})
	}
	for _, s := range cfg.ServerDefs() {
		if s.Role != utils.RoleBuilder {
			continue
		}
		cacheName := names.BuilderCacheVolumeShort(s.Name)
		detail := fmt.Sprintf("%dGB on %s (builder cache)", utils.BuilderCacheVolumeSizeGB, s.Name)
		if !liveVols[cacheName] {
			entries = append(entries, PlanEntry{
				Kind: PlanAdd, Resource: ResVolume, Name: cacheName, Detail: detail,
			})
			continue
		}
		entries = append(entries, PlanEntry{
			Kind: PlanNoChange, Resource: ResVolume, Name: cacheName, Detail: detail,
		})
	}
	if snap != nil {
		sortedLive := append([]string(nil), snap.Volumes...)
		sort.Strings(sortedLive)
		for _, name := range sortedLive {
			if desiredVols[name] {
				continue
			}
			entries = append(entries, PlanEntry{
				Kind: PlanDelete, Resource: ResVolume, Name: name,
			})
		}
	}

	// --- Firewalls (existence) ---
	masters, workers, builders := splitByRole(cfg.ServerDefs())
	desiredFW := map[string]bool{}
	if len(masters) > 0 {
		desiredFW[names.MasterFirewall()] = true
	}
	if len(workers) > 0 {
		desiredFW[names.WorkerFirewall()] = true
	}
	if len(builders) > 0 {
		desiredFW[names.BuilderFirewall()] = true
	}
	liveFW := map[string]bool{}
	if snap != nil {
		for _, fw := range snap.Firewalls {
			liveFW[fw] = true
		}
	}
	desiredFWSorted := sortedKeys(desiredFW)
	for _, fw := range desiredFWSorted {
		if !liveFW[fw] {
			entries = append(entries, PlanEntry{
				Kind: PlanAdd, Resource: ResFirewall, Name: fw,
			})
			continue
		}
		entries = append(entries, PlanEntry{
			Kind: PlanNoChange, Resource: ResFirewall, Name: fw,
		})
	}
	for _, fw := range sortedKeys(liveFW) {
		if desiredFW[fw] {
			continue
		}
		entries = append(entries, PlanEntry{
			Kind: PlanDelete, Resource: ResFirewall, Name: fw,
		})
	}

	// --- Firewall rules (per-port) ---
	// Only diffed for firewalls present in BOTH desired and live.
	// New firewalls' rules are implicit in the ResFirewall add; deleted
	// firewalls' rules are implicit in the delete.
	if snap != nil {
		desiredRules := map[string]PortAllowList{}
		if len(masters) > 0 {
			masterRules, err := FirewallAllowList(ctx, cfg)
			if err != nil {
				return nil, fmt.Errorf("master firewall allow-list: %w", err)
			}
			desiredRules[names.MasterFirewall()] = masterRules
		}
		if len(workers) > 0 {
			desiredRules[names.WorkerFirewall()] = nil // base only
		}
		if len(builders) > 0 {
			desiredRules[names.BuilderFirewall()] = nil // base only
		}
		for _, fw := range desiredFWSorted {
			if !liveFW[fw] {
				continue // new firewall — covered by ResFirewall add
			}
			entries = append(entries, diffRules(fw, desiredRules[fw], snap.FirewallRules[fw])...)
		}
	}

	return entries, nil
}

// diffRules emits PlanEntry per port that's added / removed / changed
// between the desired and live PortAllowList for one firewall.
// Internal cluster ports (6443/10250/8472/5000) are stripped upstream
// by GetFirewallRules. SSH (22) is the special case handled here:
// nvoi always opens port 22 in buildFirewallRules whether the user
// configured it or not, so it shows up in `live` unconditionally.
// `desired` only contains "22" when the user explicitly overrode it
// in `firewall:`. Treating absence-from-desired as DELETE would
// always (incorrectly) emit a "remove SSH access" entry on every
// deploy. Fix: when desired doesn't reference 22, treat live's 22
// as nvoi-managed and skip the diff entirely.
func diffRules(fwName string, desired, live PortAllowList) []PlanEntry {
	var entries []PlanEntry

	for _, port := range SortedPorts(desired) {
		liveCIDRs, exists := live[port]
		if !exists {
			entries = append(entries, PlanEntry{
				Kind:     PlanAdd,
				Resource: ResFirewallRule,
				Name:     fwName + ":" + port,
				Detail:   fmt.Sprintf("%v", desired[port]),
			})
			continue
		}
		if !cidrEqual(liveCIDRs, desired[port]) {
			entries = append(entries, PlanEntry{
				Kind:     PlanUpdate,
				Resource: ResFirewallRule,
				Name:     fwName + ":" + port,
				Detail:   fmt.Sprintf("%v → %v", liveCIDRs, desired[port]),
			})
			continue
		}
		entries = append(entries, PlanEntry{
			Kind:     PlanNoChange,
			Resource: ResFirewallRule,
			Name:     fwName + ":" + port,
			Detail:   fmt.Sprintf("%v", liveCIDRs),
		})
	}
	for _, port := range SortedPorts(live) {
		if _, exists := desired[port]; exists {
			continue
		}
		// SSH (22) is nvoi-managed by default. buildFirewallRules always
		// emits it; absence from desired ≠ "user wants it gone". Skip
		// so we don't false-flag every deploy with a "removes SSH"
		// destructive entry.
		if port == "22" {
			continue
		}
		entries = append(entries, PlanEntry{
			Kind:     PlanDelete,
			Resource: ResFirewallRule,
			Name:     fwName + ":" + port,
			Detail:   fmt.Sprintf("was %v ⚠ removes existing access", live[port]),
		})
	}

	return entries
}

// cidrEqual compares two CIDR slices ignoring order.
func cidrEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	aSorted := append([]string(nil), a...)
	bSorted := append([]string(nil), b...)
	sort.Strings(aSorted)
	sort.Strings(bSorted)
	for i := range aSorted {
		if aSorted[i] != bSorted[i] {
			return false
		}
	}
	return true
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// splitByRole groups ServerSpecs by role. Mirrors the per-cloud
// splitServers helpers (each provider package has its own copy because
// pkg/provider can't import provider-side helpers — circular). Defined
// here so ComputeInfraPlan stays free of per-provider helpers.
func splitByRole(defs []ServerSpec) (masters, workers, builders []ServerSpec) {
	for _, s := range defs {
		switch s.Role {
		case utils.RoleWorker:
			workers = append(workers, s)
		case utils.RoleBuilder:
			builders = append(builders, s)
		default:
			masters = append(masters, s)
		}
	}
	sort.Slice(masters, func(i, j int) bool { return masters[i].Name < masters[j].Name })
	sort.Slice(workers, func(i, j int) bool { return workers[i].Name < workers[j].Name })
	sort.Slice(builders, func(i, j int) bool { return builders[i].Name < builders[j].Name })
	return
}
