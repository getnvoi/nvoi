package reconcile

import (
	"context"

	"github.com/getnvoi/nvoi/internal/config"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/utils"
)

func Volumes(ctx context.Context, dc *config.DeployContext, live *config.LiveState, cfg *config.AppConfig) error {
	for _, name := range utils.SortedKeys(cfg.Volumes) {
		vol := cfg.Volumes[name]
		if _, err := app.VolumeSet(ctx, app.VolumeSetRequest{
			Cluster: dc.Cluster, Name: name, Size: vol.Size, Server: vol.Server,
		}); err != nil {
			return err
		}
	}
	if live != nil {
		desired := toSet(utils.SortedKeys(cfg.Volumes))
		for _, name := range live.Volumes {
			if !desired[name] {
				_ = app.VolumeDelete(ctx, app.VolumeDeleteRequest{Cluster: dc.Cluster, Name: name})
			}
		}
	}
	return nil
}
