package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/internal/packages"
	"github.com/getnvoi/nvoi/internal/reconcile"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// Teardown nukes external provider resources. Kubernetes resources (services,
// crons, ingress, secrets) live on the cluster and die with the servers.
//
// Where possible, teardown reuses reconcile functions with an empty desired
// state — the orphan detection loop deletes everything. Resources with
// teardown-specific policy (conditional flags, config-based deletion) use
// direct loops.
//
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

	// DNS — config-based. Provider API, no cluster query needed.
	for _, svcName := range utils.SortedKeys(cfg.Domains) {
		collect(app.DNSDelete(ctx, app.DNSDeleteRequest{
			Cluster: dc.Cluster, DNS: dc.DNS,
			Service: svcName, Domains: cfg.Domains[svcName],
		}))
	}

	// Storage — conditional, config-based.
	if deleteStorage {
		for _, name := range utils.SortedKeys(cfg.Storage) {
			collect(app.StorageEmpty(ctx, app.StorageEmptyRequest{
				Cluster: app.Cluster{AppName: dc.Cluster.AppName, Env: dc.Cluster.Env, Output: dc.Cluster.Output},
				Storage: dc.Storage, Name: name,
			}))
			collect(app.StorageDelete(ctx, app.StorageDeleteRequest{Cluster: dc.Cluster, Storage: dc.Storage, Name: name}))
		}
	}

	// Package resources (database backup buckets, etc.)
	packages.TeardownAll(ctx, dc, cfg, deleteStorage)

	// Volumes — conditional. Reuses reconcile with empty desired set.
	if deleteVolumes {
		empty := &config.AppConfig{App: cfg.App, Env: cfg.Env}
		empty.Resolve()
		live := &config.LiveState{}
		// Populate live volumes from config — teardown knows what exists
		for name := range cfg.Volumes {
			live.Volumes = append(live.Volumes, name)
		}
		collect(reconcile.Volumes(ctx, dc, live, empty))
	}

	// Servers — workers first, then master. No drain (cluster is dying).
	masters, workers := reconcile.SplitServers(cfg.Servers)
	for _, s := range workers {
		collect(app.ComputeDelete(ctx, app.ComputeDeleteRequest{Cluster: dc.Cluster, Name: s.Name}))
	}
	for _, s := range masters {
		collect(app.ComputeDelete(ctx, app.ComputeDeleteRequest{Cluster: dc.Cluster, Name: s.Name}))
	}

	// Firewalls — reuses shared orphan removal. desired=nil deletes all.
	names, _ := utils.NewNames(cfg.App, cfg.Env)
	if names != nil {
		for _, err := range app.FirewallRemoveOrphans(ctx, app.FirewallRemoveOrphansRequest{
			Cluster: dc.Cluster,
			Prefix:  names.Base() + "-",
			Desired: nil,
		}) {
			collect(err)
		}
	}

	// Network — always nuked.
	collect(app.NetworkDelete(ctx, app.NetworkDeleteRequest{Cluster: dc.Cluster}))

	if len(errs) > 0 {
		return fmt.Errorf("teardown completed with %d error(s):\n  %s", len(errs), strings.Join(errs, "\n  "))
	}
	return nil
}
