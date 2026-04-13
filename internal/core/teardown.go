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
	"github.com/spf13/cobra"
)

func NewTeardownCmd(dc *config.DeployContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "teardown",
		Short: "Tear down all provider resources in config YAML",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			deleteVolumes, _ := cmd.Flags().GetBool("delete-volumes")
			deleteStorage, _ := cmd.Flags().GetBool("delete-storage")
			return teardown(cmd.Context(), dc, cfg, deleteVolumes, deleteStorage)
		},
	}
	cmd.Flags().Bool("delete-volumes", false, "also delete persistent volumes (preserved by default)")
	cmd.Flags().Bool("delete-storage", false, "also delete storage buckets (preserved by default)")
	return cmd
}

// teardown nukes external provider resources. Kubernetes resources (services,
// crons, ingress, secrets) live on the cluster and die with the servers.
// K8s resource management is reconcile's job, not teardown's.
// Best-effort: continues through all resources, collects and returns all errors.
func teardown(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig, deleteVolumes, deleteStorage bool) error {
	var errs []string
	collect := func(err error) {
		if err != nil {
			errs = append(errs, err.Error())
		}
	}

	// DNS records — external, at the DNS provider
	for _, svcName := range utils.SortedKeys(cfg.Domains) {
		collect(app.DNSDelete(ctx, app.DNSDeleteRequest{
			Cluster: dc.Cluster, DNS: dc.DNS,
			Service: svcName, Domains: cfg.Domains[svcName],
		}))
	}

	// Storage buckets — external, preserved by default
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

	// Volumes — external, preserved by default
	if deleteVolumes {
		for _, name := range utils.SortedKeys(cfg.Volumes) {
			collect(app.VolumeDelete(ctx, app.VolumeDeleteRequest{Cluster: dc.Cluster, Name: name}))
		}
	}

	// Servers — workers first, then master
	masters, workers := reconcile.SplitServers(cfg.Servers)
	for _, s := range workers {
		collect(app.ComputeDelete(ctx, app.ComputeDeleteRequest{Cluster: dc.Cluster, Name: s.Name}))
	}
	for _, s := range masters {
		collect(app.ComputeDelete(ctx, app.ComputeDeleteRequest{Cluster: dc.Cluster, Name: s.Name}))
	}

	// Firewall + network — shared provider resources, always nuked
	collect(app.FirewallDelete(ctx, app.FirewallDeleteRequest{Cluster: dc.Cluster}))
	collect(app.NetworkDelete(ctx, app.NetworkDeleteRequest{Cluster: dc.Cluster}))

	if len(errs) > 0 {
		return fmt.Errorf("teardown completed with %d error(s):\n  %s", len(errs), strings.Join(errs, "\n  "))
	}
	return nil
}
