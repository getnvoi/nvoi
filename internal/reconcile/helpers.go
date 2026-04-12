package reconcile

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/getnvoi/nvoi/internal/config"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// DescribeLive queries the cluster and provider for current state.
// Returns nil if nothing exists (first deploy).
func DescribeLive(ctx context.Context, dc *config.DeployContext) *config.LiveState {
	res, err := app.Describe(ctx, app.DescribeRequest{Cluster: dc.Cluster})
	if err != nil {
		return nil
	}
	servers, _ := app.ComputeList(ctx, app.ComputeListRequest{Cluster: dc.Cluster})
	volumes, _ := app.VolumeList(ctx, app.VolumeListRequest{Cluster: dc.Cluster})

	names, _ := dc.Cluster.Names()
	prefix := names.Base() + "-"
	strip := func(s string) string {
		if len(s) > len(prefix) && s[:len(prefix)] == prefix {
			return s[len(prefix):]
		}
		return s
	}

	state := &config.LiveState{Domains: map[string][]string{}}
	seen := map[string]bool{}
	for _, s := range servers {
		name := strip(s.Name)
		if !seen[name] {
			state.Servers = append(state.Servers, name)
			seen[name] = true
		}
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
	for _, v := range volumes {
		state.Volumes = append(state.Volumes, strip(v.Name))
	}
	for _, s := range res.Storage {
		state.Storage = append(state.Storage, s.Name)
	}
	for _, s := range res.Secrets {
		state.Secrets = append(state.Secrets, s.Key)
	}
	for _, i := range res.Ingress {
		state.Domains[i.Service] = append(state.Domains[i.Service], i.Domain)
	}
	return state
}

func drainNode(ctx context.Context, dc *config.DeployContext, name string) {
	names, err := dc.Cluster.Names()
	if err != nil {
		return
	}
	ssh, _, err := dc.Cluster.SSH(ctx)
	if err != nil {
		return
	}
	defer ssh.Close()
	dc.Cluster.Log().Command("node", "drain", names.Server(name))
	kube.DrainAndRemoveNode(ctx, ssh, names.Server(name))
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

func resolveImageRef(ctx context.Context, dc *config.DeployContext, image, buildRef string) (string, error) {
	if buildRef != "" {
		ref, err := app.BuildLatest(ctx, app.BuildLatestRequest{Cluster: dc.Cluster, Name: buildRef})
		if err != nil {
			return "", fmt.Errorf("resolve build %q: %w", buildRef, err)
		}
		return ref, nil
	}
	return image, nil
}

func buildTargetStrings(build map[string]string) []string {
	var targets []string
	for _, name := range utils.SortedKeys(build) {
		targets = append(targets, name+":"+build[name])
	}
	return targets
}

func toSet(items []string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, item := range items {
		m[item] = true
	}
	return m
}
