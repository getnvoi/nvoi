package reconcile

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/internal/config"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/utils"
)

func Crons(ctx context.Context, dc *config.DeployContext, live *config.LiveState, cfg *config.AppConfig, packageEnvVars map[string]string) error {
	for _, name := range utils.SortedKeys(cfg.Crons) {
		cron := cfg.Crons[name]
		image, err := resolveImageRef(ctx, dc, cron.Image, cron.Build)
		if err != nil {
			return err
		}
		servers := ResolveServers(cfg, cron.Servers, cron.Server, cron.Volumes)
		envVars := append([]string{}, cron.Env...)
		for k, v := range packageEnvVars {
			envVars = append(envVars, k+"="+v)
		}
		if err := app.CronSet(ctx, app.CronSetRequest{
			Cluster: dc.Cluster, Name: name, Image: image,
			Command: cron.Command, EnvVars: envVars, Secrets: cron.Secrets,
			Storages: cron.Storage, Volumes: cron.Volumes,
			Schedule: cron.Schedule, Servers: servers,
		}); err != nil {
			return err
		}
	}
	if live != nil {
		desired := toSet(utils.SortedKeys(cfg.Crons))
		protected := map[string]bool{}
		for dbName := range cfg.Database {
			protected[dbName+"-db-backup"] = true
		}
		for _, name := range live.Crons {
			if !desired[name] && !protected[name] {
				if err := app.CronDelete(ctx, app.CronDeleteRequest{Cluster: dc.Cluster, Name: name}); err != nil {
					dc.Cluster.Log().Warning(fmt.Sprintf("orphan cron %s not removed: %s", name, err))
				}
			}
		}
	}
	return nil
}
