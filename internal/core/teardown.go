package core

import (
	"context"

	"github.com/getnvoi/nvoi/internal/reconcile"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/utils"
	"github.com/spf13/cobra"
)

func NewTeardownCmd(dc *reconcile.DeployContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "teardown",
		Short: "Tear down all resources in config YAML",
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

func teardown(ctx context.Context, dc *reconcile.DeployContext, cfg *reconcile.AppConfig, deleteVolumes, deleteStorage bool) error {
	// Networking
	for _, svcName := range utils.SortedKeys(cfg.Domains) {
		_ = app.IngressDelete(ctx, app.IngressDeleteRequest{
			Cluster: dc.Cluster,
			Route:   app.IngressRouteArg{Service: svcName, Domains: cfg.Domains[svcName]},
		})
		_ = app.DNSDelete(ctx, app.DNSDeleteRequest{
			Cluster: dc.Cluster, DNS: dc.DNS,
			Service: svcName, Domains: cfg.Domains[svcName],
		})
	}

	// Workloads
	for _, name := range utils.SortedKeys(cfg.Crons) {
		_ = app.CronDelete(ctx, app.CronDeleteRequest{Cluster: dc.Cluster, Name: name})
	}
	for _, name := range utils.SortedKeys(cfg.Services) {
		_ = app.ServiceDelete(ctx, app.ServiceDeleteRequest{Cluster: dc.Cluster, Name: name})
	}

	// Storage — preserved by default
	if deleteStorage {
		for _, name := range utils.SortedKeys(cfg.Storage) {
			_ = app.StorageEmpty(ctx, app.StorageEmptyRequest{
				Cluster: app.Cluster{AppName: dc.Cluster.AppName, Env: dc.Cluster.Env, Output: dc.Cluster.Output},
				Storage: dc.Storage, Name: name,
			})
			_ = app.StorageDelete(ctx, app.StorageDeleteRequest{Cluster: dc.Cluster, Storage: dc.Storage, Name: name})
		}
	}

	// Secrets
	for _, key := range cfg.Secrets {
		_ = app.SecretDelete(ctx, app.SecretDeleteRequest{Cluster: dc.Cluster, Key: key})
	}

	// Volumes — preserved by default
	if deleteVolumes {
		for _, name := range utils.SortedKeys(cfg.Volumes) {
			_ = app.VolumeDelete(ctx, app.VolumeDeleteRequest{Cluster: dc.Cluster, Name: name})
		}
	}

	// Servers — workers first, then master
	masters, workers := reconcile.SplitServers(cfg.Servers)
	for _, s := range workers {
		_ = app.ComputeDelete(ctx, app.ComputeDeleteRequest{Cluster: dc.Cluster, Name: s.Name})
	}
	for _, s := range masters {
		_ = app.ComputeDelete(ctx, app.ComputeDeleteRequest{Cluster: dc.Cluster, Name: s.Name})
	}

	// Nuke shared provider resources — firewall and network.
	// Only destroy does this. Reconcile never touches these.
	names, err := dc.Cluster.Names()
	if err != nil {
		return nil
	}
	prov, err := dc.Cluster.Compute()
	if err != nil {
		return nil
	}
	_ = prov.DeleteFirewall(ctx, names.Firewall())
	_ = prov.DeleteNetwork(ctx, names.Network())

	return nil
}
