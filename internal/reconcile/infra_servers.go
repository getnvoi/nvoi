package reconcile

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/internal/config"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// ServersAdd creates desired servers.
// Bootstrap (Kube nil): masters only — the laptop never touches workers.
// Agent (Kube set): workers only — the master already exists (axiomatic).
// Does NOT remove orphans — that's deferred to ServersRemoveOrphans
// after workloads have been moved to the new nodes.
func ServersAdd(ctx context.Context, dc *config.DeployContext, live *config.LiveState, cfg *config.AppConfig) error {
	masters, workers := SplitServers(cfg.Servers)
	var servers []config.NamedServer
	if dc.Cluster.Kube == nil {
		// Bootstrap: provision masters only. Workers come from the agent.
		servers = masters
	} else {
		// Agent: provision workers only. Master exists — that's how the agent is running.
		servers = workers
	}
	for _, s := range servers {
		// Disk is creation-only. If the server already exists with a different
		// disk size, fail fast — EnsureServer won't resize it.
		if s.Disk > 0 && live != nil && live.ServerDisk != nil {
			if liveDisk, exists := live.ServerDisk[s.Name]; exists && liveDisk != s.Disk {
				return fmt.Errorf("servers.%s.disk: root disk is %d GB but config wants %d GB — root disk cannot be resized, recreate the server to apply", s.Name, liveDisk, s.Disk)
			}
		}

		creds := dc.Cluster.Credentials
		if s.Region != "" {
			creds = copyMap(creds)
			creds["region"] = s.Region
		}
		connectSSH := dc.ConnectSSH
		if connectSSH == nil {
			connectSSH = sshConnector(dc.Cluster.SSHKey)
		}
		if _, err := app.ComputeSet(ctx, app.ComputeSetRequest{
			Cluster: clusterWith(dc, creds), Output: dc.Output,
			ConnectSSH: connectSSH, Name: s.Name,
			ServerType: s.Type, Region: s.Region,
			Worker: s.Role == "worker", DiskGB: s.Disk,
		}); err != nil {
			return err
		}
	}
	return nil
}

// ServersRemoveOrphans drains and deletes servers that are in live state
// but not in the desired config. Called AFTER services/crons have been
// reconciled so workloads have already moved to new nodes.
// If drain fails, the server is NOT deleted and the error is returned.
func ServersRemoveOrphans(ctx context.Context, dc *config.DeployContext, live *config.LiveState, cfg *config.AppConfig) error {
	if live == nil {
		return nil
	}
	desired := toSet(utils.SortedKeys(cfg.Servers))
	for _, name := range live.Servers {
		if !desired[name] {
			if err := drainNode(ctx, dc, name); err != nil {
				return fmt.Errorf("cannot remove server %s: %w", name, err)
			}
			if err := app.ComputeDelete(ctx, app.ComputeDeleteRequest{Cluster: dc.Cluster, Output: dc.Output, Name: name}); err != nil {
				return err
			}
		}
	}
	return nil
}
