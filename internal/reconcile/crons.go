package reconcile

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/internal/config"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/utils"
)

func Crons(ctx context.Context, dc *config.DeployContext, live *config.LiveState, cfg *config.AppConfig, sources map[string]string) error {
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
			Cluster: dc.Cluster, Name: name, Image: cron.Image,
			Command:    cron.Command,
			EnvVars:    plainEnv,
			SvcSecrets: svcSecretRefs,
			Volumes:    cron.Volumes,
			Schedule:   cron.Schedule, Servers: servers,
			PullSecretName: pullSecret,
		}); err != nil {
			return err
		}
	}
	if live != nil {
		desired := toSet(cronNames)
		for _, name := range live.Crons {
			if !desired[name] {
				if err := app.CronDelete(ctx, app.CronDeleteRequest{Cluster: dc.Cluster, Name: name}); err != nil {
					dc.Cluster.Log().Warning(fmt.Sprintf("orphan cron %s not removed: %s", name, err))
				}
				deleteServiceSecret(ctx, dc, names, name)
			}
		}
	}
	return nil
}
