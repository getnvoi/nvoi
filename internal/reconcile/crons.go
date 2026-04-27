package reconcile

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/internal/config"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

func Crons(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig, sources map[string]string) error {
	names, _ := dc.Cluster.Names()

	pullSecret := ""
	if len(cfg.Registry) > 0 {
		pullSecret = kube.PullSecretName
	}

	cronNames := utils.SortedKeys(cfg.Crons)
	for _, name := range cronNames {
		cron := cfg.Crons[name]
		servers := ResolveServers(cfg, cron.Servers, cron.Server, cron.Volumes)

		// Resolve env: entries — plain text in manifest
		plainEnv := make([]string, 0, len(cron.Env))
		for _, entry := range cron.Env {
			k, v, err := resolveEntry(entry, sources)
			if err != nil {
				return fmt.Errorf("crons.%s.env: %w", name, err)
			}
			plainEnv = append(plainEnv, k+"="+v)
		}

		// Resolve secrets: entries — stored in per-cron k8s Secret
		svcSecretKVs, svcSecretRefs, err := resolveSecretEntries(name, cron.Secrets, sources)
		if err != nil {
			return err
		}

		// Expand storage: into per-cron secret entries
		expandStorageCreds(cron.Storage, sources, svcSecretKVs, &svcSecretRefs)

		if err := upsertServiceSecrets(ctx, dc, names, name, svcSecretKVs); err != nil {
			return err
		}

		if err := app.CronSet(ctx, app.CronSetRequest{
			Cluster: dc.Cluster, Cfg: config.NewView(cfg),
			Name: name, Image: cron.Image,
			Command:    cron.Command,
			EnvVars:    plainEnv,
			SvcSecrets: svcSecretRefs,
			Volumes:    cron.Volumes,
			Schedule:   cron.Schedule, Servers: servers,
			PullSecretName: pullSecret,
			KnownVolumes:   knownVolumes(cfg),
		}); err != nil {
			return err
		}
	}

	// Orphan sweep — owner-scoped, so DB backup CronJobs (owner=
	// databases) can never be seen here. Per-cron secrets follow the
	// same `<name>-secrets` shape as services.
	ns := names.KubeNamespace()
	kc := dc.Cluster.MasterKube
	desiredSecrets := make([]string, 0, len(cronNames))
	for _, n := range cronNames {
		desiredSecrets = append(desiredSecrets, names.KubeServiceSecrets(n))
	}
	if err := provider.SweepOwned(ctx, kc, ns, provider.KindCronWorkload, kube.KindCronJob, cronNames); err != nil {
		dc.Cluster.Log().Warning(fmt.Sprintf("crons sweep cronjobs: %s", err))
	}
	if err := provider.SweepOwned(ctx, kc, ns, provider.KindCronWorkload, kube.KindSecret, desiredSecrets); err != nil {
		dc.Cluster.Log().Warning(fmt.Sprintf("crons sweep secrets: %s", err))
	}
	return nil
}
