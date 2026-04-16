package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/internal/packages"
	"github.com/getnvoi/nvoi/internal/reconcile"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/infra"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// Teardown nukes external provider resources. Kubernetes resources (services,
// crons, ingress, secrets) live on the cluster and die with the servers.
// K8s resource management is reconcile's job, not teardown's.
// Best-effort: continues through all resources, collects and returns all errors.
func Teardown(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig, deleteVolumes, deleteStorage bool) error {
	if err := cfg.Resolve(); err != nil {
		return err
	}
	var errs []string
	collect := func(err error) {
		if err != nil {
			errs = append(errs, err.Error())
		}
	}

	out := dc.Output

	// DNS records — external, at the DNS provider
	for _, svcName := range utils.SortedKeys(cfg.Domains) {
		collect(app.DNSDelete(ctx, app.DNSDeleteRequest{
			Cluster: dc.Cluster, Output: out, DNS: dc.DNS,
			Service: svcName, Domains: cfg.Domains[svcName],
		}))
	}

	// Storage buckets — external, preserved by default
	if deleteStorage {
		for _, name := range utils.SortedKeys(cfg.Storage) {
			collect(app.StorageEmpty(ctx, app.StorageEmptyRequest{
				Cluster: app.Cluster{AppName: dc.Cluster.AppName, Env: dc.Cluster.Env},
				Output:  out, Storage: dc.Storage, Name: name,
			}))
			collect(app.StorageDelete(ctx, app.StorageDeleteRequest{Cluster: dc.Cluster, Output: out, Storage: dc.Storage, Name: name}))
		}
	}

	// Package resources (database backup buckets, etc.)
	packages.TeardownAll(ctx, dc, cfg, deleteStorage)

	// Volumes — external, preserved by default
	if deleteVolumes {
		connectSSH := dc.ConnectSSH
		if connectSSH == nil {
			connectSSH = sshConnector(dc.Cluster.SSHKey)
		}
		for _, name := range utils.SortedKeys(cfg.Volumes) {
			collect(app.VolumeDelete(ctx, app.VolumeDeleteRequest{
				Cluster: dc.Cluster, Output: out,
				ConnectSSH: connectSSH, Name: name,
			}))
		}
	}

	// Servers — workers first, then master
	masters, workers := reconcile.SplitServers(cfg.Servers)
	for _, s := range workers {
		collect(app.ComputeDelete(ctx, app.ComputeDeleteRequest{Cluster: dc.Cluster, Output: out, Name: s.Name}))
	}
	for _, s := range masters {
		collect(app.ComputeDelete(ctx, app.ComputeDeleteRequest{Cluster: dc.Cluster, Output: out, Name: s.Name}))
	}

	// Firewalls — nuke all matching our prefix (desired=nil = delete everything)
	names, _ := utils.NewNames(cfg.App, cfg.Env)
	if names != nil {
		for _, err := range app.FirewallRemoveOrphans(ctx, app.FirewallRemoveOrphansRequest{
			Cluster: dc.Cluster, Output: out,
			Prefix:  names.Base() + "-",
			Desired: nil,
		}) {
			collect(err)
		}
	}

	// Network — always nuked
	collect(app.NetworkDelete(ctx, app.NetworkDeleteRequest{Cluster: dc.Cluster, Output: out}))

	if len(errs) > 0 {
		return fmt.Errorf("teardown completed with %d error(s):\n  %s", len(errs), strings.Join(errs, "\n  "))
	}
	return nil
}

func sshConnector(sshKey []byte) app.ConnectSSH {
	return func(ctx context.Context, addr string) (utils.SSHClient, error) {
		return infra.ConnectSSH(ctx, addr, utils.DefaultUser, sshKey)
	}
}
