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
//  4. Tunnel delete — after infra so tunnel agents are gone before the
//     provider-side tunnel is removed.
//
// Best-effort: each step's errors are collected and surfaced together.
func Teardown(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig, deleteVolumes, deleteStorage, deleteDatabases bool) error {
	if err := cfg.Resolve(); err != nil {
		return err
	}
	var errs []string
	collect := func(err error) {
		if err != nil {
			errs = append(errs, err.Error())
		}
	}

	// DNS records — external, at the DNS provider. Direct call here
	// (no app.DNSDelete wrapper any more — that was deleted in C10
	// alongside the rest of the dead pkg/core wrappers). Each unroute is
	// idempotent at the provider; missing records are not an error.
	if dc.DNS.Name != "" && len(cfg.Domains) > 0 {
		dns, err := provider.ResolveDNS(dc.DNS.Name, dc.DNS.Creds)
		if err != nil {
			collect(fmt.Errorf("resolve dns provider: %w", err))
		} else {
			for _, svcName := range utils.SortedKeys(cfg.Domains) {
				dc.Cluster.Log().Command("dns", "delete", svcName)
				for _, domain := range cfg.Domains[svcName] {
					if err := dns.Unroute(ctx, domain); err != nil {
						collect(fmt.Errorf("dns unroute %s: %w", domain, err))
					} else {
						dc.Cluster.Log().Success(fmt.Sprintf("%s deleted", domain))
					}
				}
			}
		}
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

	if deleteDatabases {
		names, nerr := utils.NewNames(cfg.App, cfg.Env)
		if nerr != nil {
			collect(nerr)
		} else {
			for _, name := range utils.SortedKeys(cfg.Databases) {
				def := cfg.Databases[name]
				schema, serr := provider.GetSchema("database", def.Engine)
				if serr != nil {
					collect(fmt.Errorf("database %s schema: %w", name, serr))
					continue
				}
				creds, cerr := provider.ResolveFrom(schema, dc.Creds)
				if cerr != nil {
					collect(fmt.Errorf("database %s creds: %w", name, cerr))
					continue
				}
				db, derr := provider.ResolveDatabase(def.Engine, creds)
				if derr != nil {
					collect(fmt.Errorf("database %s provider: %w", name, derr))
					continue
				}
				req := provider.DatabaseRequest{
					Name:                  name,
					FullName:              names.Database(name),
					PodName:               names.KubeDatabasePod(name),
					PVCName:               names.KubeDatabasePVC(name),
					BackupName:            names.KubeDatabaseBackupCron(name),
					CredentialsSecretName: names.KubeDatabaseCredentials(name),
					Namespace:             names.KubeNamespace(),
					Labels:                names.Labels(),
					DeleteVolumes:         deleteVolumes || deleteDatabases,
				}
				collect(db.Delete(ctx, req))
				_ = db.Close()
			}
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

	// Tunnel — delete the provider-side tunnel after infra teardown so
	// cluster-side tunnel agents are already gone. Cloudflare rejects
	// tunnel deletion while active connections still exist. ngrok models
	// ingress as per-hostname reserved domains, so teardown deletes each
	// configured hostname explicitly; this also cleans up legacy domains
	// that predate the metadata tagging added in the provider.
	if cfg.Providers.Tunnel != "" && dc.Tunnel.Name != "" {
		tun, terr := provider.ResolveTunnel(dc.Tunnel.Name, dc.Tunnel.Creds)
		if terr != nil {
			collect(fmt.Errorf("resolve tunnel provider: %w", terr))
		} else if cfg.Providers.Tunnel == "ngrok" {
			for _, svcName := range utils.SortedKeys(cfg.Domains) {
				for _, domain := range cfg.Domains[svcName] {
					dc.Cluster.Log().Command("tunnel", "delete-domain", domain)
					collect(tun.Delete(ctx, domain))
				}
			}
		} else {
			tunnelNames, err := utils.NewNames(cfg.App, cfg.Env)
			if err == nil {
				dc.Cluster.Log().Command("tunnel", "delete", tunnelNames.Base())
				collect(tun.Delete(ctx, tunnelNames.Base()))
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("teardown completed with %d error(s):\n  %s", len(errs), strings.Join(errs, "\n  "))
	}
	return nil
}
