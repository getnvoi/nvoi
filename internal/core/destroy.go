package core

import (
	"context"

	"github.com/getnvoi/nvoi/internal/reconcile"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/utils"
	"github.com/spf13/cobra"
)

func NewDestroyCmd(dc *reconcile.DeployContext) *cobra.Command {
	return &cobra.Command{
		Use:   "destroy",
		Short: "Destroy all resources in config YAML",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			return destroy(cmd.Context(), dc, cfg)
		},
	}
}

func destroy(ctx context.Context, dc *reconcile.DeployContext, cfg *reconcile.AppConfig) error {
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
	for _, name := range utils.SortedKeys(cfg.Crons) {
		_ = app.CronDelete(ctx, app.CronDeleteRequest{Cluster: dc.Cluster, Name: name})
	}
	for _, name := range utils.SortedKeys(cfg.Services) {
		_ = app.ServiceDelete(ctx, app.ServiceDeleteRequest{Cluster: dc.Cluster, Name: name})
	}
	for _, name := range utils.SortedKeys(cfg.Storage) {
		_ = app.StorageEmpty(ctx, app.StorageEmptyRequest{
			Cluster: app.Cluster{AppName: dc.Cluster.AppName, Env: dc.Cluster.Env, Output: dc.Cluster.Output},
			Storage: dc.Storage, Name: name,
		})
		_ = app.StorageDelete(ctx, app.StorageDeleteRequest{Cluster: dc.Cluster, Storage: dc.Storage, Name: name})
	}
	for _, key := range cfg.Secrets {
		_ = app.SecretDelete(ctx, app.SecretDeleteRequest{Cluster: dc.Cluster, Key: key})
	}
	for _, name := range utils.SortedKeys(cfg.Volumes) {
		_ = app.VolumeDelete(ctx, app.VolumeDeleteRequest{Cluster: dc.Cluster, Name: name})
	}
	masters, workers := reconcile.SplitServers(cfg.Servers)
	for _, s := range workers {
		_ = app.ComputeDelete(ctx, app.ComputeDeleteRequest{Cluster: dc.Cluster, Name: s.Name})
	}
	for _, s := range masters {
		_ = app.ComputeDelete(ctx, app.ComputeDeleteRequest{Cluster: dc.Cluster, Name: s.Name})
	}
	return nil
}
