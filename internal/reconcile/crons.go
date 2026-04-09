package reconcile

import (
	"context"

	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/utils"
)

func Crons(ctx context.Context, dc *DeployContext, live *LiveState, cfg *AppConfig) error {
	for _, name := range utils.SortedKeys(cfg.Crons) {
		cron := cfg.Crons[name]
		image, err := resolveImageRef(ctx, dc, cron.Image, cron.Build)
		if err != nil {
			return err
		}
		if err := app.CronSet(ctx, app.CronSetRequest{
			Cluster: dc.Cluster, Name: name, Image: image,
			Command: cron.Command, EnvVars: cron.Env, Secrets: cron.Secrets,
			Storages: cron.Storage, Volumes: cron.Volumes,
			Schedule: cron.Schedule, Server: cron.Server,
		}); err != nil {
			return err
		}
	}
	if live != nil {
		desired := toSet(utils.SortedKeys(cfg.Crons))
		for _, name := range live.Crons {
			if !desired[name] {
				_ = app.CronDelete(ctx, app.CronDeleteRequest{Cluster: dc.Cluster, Name: name})
			}
		}
	}
	return nil
}
