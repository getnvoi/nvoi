package reconcile

import (
	"strings"

	"github.com/getnvoi/nvoi/internal/config"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// knownVolumes returns the volume short-names ServiceSet / CronSet can
// trust to exist. Source: cfg.Volumes alone — by the time Services /
// Crons run in reconcile.Deploy, infra.Bootstrap has already provisioned
// every volume in cfg.Volumes (idempotent on existing volumes). No
// provider lookup needed.
func knownVolumes(cfg *config.AppConfig) []string {
	out := make([]string, 0, len(cfg.Volumes))
	for name := range cfg.Volumes {
		out = append(out, name)
	}
	return out
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

// SplitServers separates masters and workers, sorted by name.
// utils.SortedKeys iterates the map in sorted order, so workers and masters
// end up in alphabetical order without a second pass.
func SplitServers(servers map[string]config.ServerDef) (masters, workers []config.NamedServer) {
	for _, name := range utils.SortedKeys(servers) {
		s := config.NamedServer{Name: name, ServerDef: servers[name]}
		if s.Role == utils.RoleWorker {
			workers = append(workers, s)
		} else {
			masters = append(masters, s)
		}
	}
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
