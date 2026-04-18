package reconcile

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/getnvoi/nvoi/internal/config"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// isKubeconfigMissing returns true when err originates from kube.Client
// failing to fetch /home/deploy/.kube/config. This means k3s hasn't
// finished installing yet — we're mid-first-deploy, not an active cluster
// with a corrupt kubeconfig.
func isKubeconfigMissing(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, ".kube/config") &&
		strings.Contains(msg, "No such file or directory")
}

// DescribeLive queries the cluster and provider for current state.
// Returns (nil, nil) on first deploy (no provider-side resources).
// Returns error if provider state exists but cluster state can't be read —
// prevents silent orphan accumulation from flaky SSH.
//
// Provider-side state goes through infra.LiveSnapshot. Kube-side state
// (workloads, crons, ingress) goes through app.Describe via the kube
// tunnel. The two halves are merged into config.LiveState.
func DescribeLive(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig) (*config.LiveState, error) {
	// Resolve infra provider once and ask it for its live view.
	bctx := config.BootstrapContext(dc, cfg)
	prov, providerErr := provider.ResolveInfra(bctx.ProviderName, dc.Cluster.Credentials)
	var snap *provider.LiveSnapshot
	if providerErr == nil {
		s, err := prov.LiveSnapshot(ctx, bctx)
		if err == nil {
			snap = s
		} else {
			providerErr = err
		}
	}

	res, err := app.Describe(ctx, app.DescribeRequest{
		Cluster:        dc.Cluster,
		StorageNames:   cfg.StorageNames(),
		ServiceSecrets: cfg.ServiceSecrets(),
	})
	if err != nil {
		if providerErr != nil {
			// Both calls failed — cannot distinguish "first deploy" from "API unreachable."
			return nil, fmt.Errorf("cannot determine cluster state — provider snapshot failed: %w", providerErr)
		}
		if snap == nil {
			return nil, nil // first deploy — nothing exists
		}
		if isKubeconfigMissing(err) {
			// Provider resources exist but k3s hasn't been installed yet
			// (prior deploy aborted mid-provisioning). Return a minimal
			// live state populated from the snapshot so Bootstrap can
			// resume.
			return liveFromSnapshot(snap), nil
		}
		return nil, fmt.Errorf("servers exist but cluster state unreadable — cannot detect orphans: %w", err)
	}

	state := liveFromSnapshot(snap)
	if state.ServerDisk == nil {
		state.ServerDisk = map[string]int{}
	}

	// Merge kube-side state.
	names, _ := dc.Cluster.Names()
	prefix := names.Base() + "-"
	strip := func(s string) string {
		if len(s) > len(prefix) && s[:len(prefix)] == prefix {
			return s[len(prefix):]
		}
		return s
	}

	seen := map[string]bool{}
	for _, s := range state.Servers {
		seen[s] = true
	}
	for _, n := range res.Nodes {
		name := strip(n.Name)
		if !seen[name] {
			state.Servers = append(state.Servers, name)
			seen[name] = true
		}
	}
	for _, w := range res.Workloads {
		state.Services = append(state.Services, w.Name)
	}
	for _, c := range res.Crons {
		state.Crons = append(state.Crons, c.Name)
	}
	for _, s := range res.Storage {
		state.Storage = append(state.Storage, s.Name)
	}
	for _, i := range res.Ingress {
		state.Domains[i.Service] = append(state.Domains[i.Service], i.Domain)
	}

	sort.Strings(state.Servers)
	sort.Strings(state.Firewalls)
	sort.Strings(state.Services)
	sort.Strings(state.Crons)
	sort.Strings(state.Volumes)
	sort.Strings(state.Storage)
	for _, domains := range state.Domains {
		sort.Strings(domains)
	}
	return state, nil
}

// liveFromSnapshot builds a LiveState skeleton from the provider snapshot.
// Returns a populated struct with empty kube-side fields ready to merge.
func liveFromSnapshot(snap *provider.LiveSnapshot) *config.LiveState {
	state := &config.LiveState{Domains: map[string][]string{}, ServerDisk: map[string]int{}}
	if snap == nil {
		return state
	}
	state.Servers = append(state.Servers, snap.Servers...)
	state.Volumes = append(state.Volumes, snap.Volumes...)
	state.Firewalls = append(state.Firewalls, snap.Firewalls...)
	for k, v := range snap.ServerDisk {
		state.ServerDisk[k] = v
	}
	return state
}

func clusterWith(dc *config.DeployContext, creds map[string]string) app.Cluster {
	c := dc.Cluster
	c.Credentials = creds
	return c
}

func copyMap(m map[string]string) map[string]string {
	cp := make(map[string]string, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

// SplitServers separates masters and workers, sorted.
func SplitServers(servers map[string]config.ServerDef) (masters, workers []config.NamedServer) {
	for _, name := range utils.SortedKeys(servers) {
		s := config.NamedServer{Name: name, ServerDef: servers[name]}
		if s.Role == "worker" {
			workers = append(workers, s)
		} else {
			masters = append(masters, s)
		}
	}
	sort.Slice(workers, func(i, j int) bool { return workers[i].Name < workers[j].Name })
	return
}

// ResolveServers returns the effective server list for a workload.
// If servers is explicit, returns it. If a single server is set, returns it.
// If the workload mounts a named volume, it's pinned to that volume's server.
func ResolveServers(cfg *config.AppConfig, servers []string, server string, mounts []string) []string {
	if len(servers) > 0 {
		return servers
	}
	if server != "" {
		return []string{server}
	}
	for _, mount := range mounts {
		volName, _, ok := strings.Cut(mount, ":")
		if !ok || strings.HasPrefix(volName, "/") || strings.HasPrefix(volName, ".") {
			continue
		}
		if vol, exists := cfg.Volumes[volName]; exists {
			return []string{vol.Server}
		}
	}
	return nil
}

func toSet(items []string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, item := range items {
		m[item] = true
	}
	return m
}
