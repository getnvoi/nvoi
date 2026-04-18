package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/internal/config"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// Teardown nukes external provider resources. Kubernetes resources
// (services, crons, ingress, secrets) live on the cluster and die with
// the servers — k8s resource management is reconcile's job, not
// teardown's.
//
// Order:
//
//  1. DNS records (external, at the DNS provider — must run first so
//     stale records don't outlive their targets while we're nuking).
//  2. Storage buckets (only with --delete-storage; preserved by default).
//  3. infra.Teardown — the provider does servers / firewalls / volumes
//     (gated by --delete-volumes) / network in the right order.
//
// Best-effort: each step's errors are collected and surfaced together.
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

	// DNS records — external, at the DNS provider.
	for _, svcName := range utils.SortedKeys(cfg.Domains) {
		collect(app.DNSDelete(ctx, app.DNSDeleteRequest{
			Cluster: dc.Cluster, DNS: dc.DNS,
			Service: svcName, Domains: cfg.Domains[svcName],
		}))
	}

	// Storage buckets — external, preserved by default.
	if deleteStorage {
		for _, name := range utils.SortedKeys(cfg.Storage) {
			collect(app.StorageEmpty(ctx, app.StorageEmptyRequest{
				Cluster: app.Cluster{AppName: dc.Cluster.AppName, Env: dc.Cluster.Env, Output: dc.Cluster.Output},
				Storage: dc.Storage, Name: name,
			}))
			collect(app.StorageDelete(ctx, app.StorageDeleteRequest{Cluster: dc.Cluster, Storage: dc.Storage, Name: name}))
		}
	}

	// Infra — servers, firewalls, optional volumes, network. Provider
	// owns the order (workers before master, firewall sweep AFTER server
	// detachment, etc.) per its DeleteServer contract.
	bctx := config.BootstrapContext(dc, cfg)
	prov, err := provider.ResolveInfra(bctx.ProviderName, dc.Cluster.Credentials)
	if err != nil {
		collect(fmt.Errorf("resolve infra provider: %w", err))
	} else {
		defer func() { _ = prov.Close() }()
		collect(prov.Teardown(ctx, bctx, deleteVolumes))
	}

	if len(errs) > 0 {
		return fmt.Errorf("teardown completed with %d error(s):\n  %s", len(errs), strings.Join(errs, "\n  "))
	}
	return nil
}
