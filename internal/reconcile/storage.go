package reconcile

import (
	"context"

	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/utils"
)

func Storage(ctx context.Context, dc *DeployContext, live *LiveState, cfg *AppConfig) error {
	for _, name := range utils.SortedKeys(cfg.Storage) {
		st := cfg.Storage[name]
		if err := app.StorageSet(ctx, app.StorageSetRequest{
			Cluster: dc.Cluster, Storage: dc.Storage,
			Name: name, Bucket: st.Bucket, CORS: st.CORS, ExpireDays: st.ExpireDays,
		}); err != nil {
			return err
		}
	}
	if live != nil {
		desired := toSet(utils.SortedKeys(cfg.Storage))
		for _, name := range live.Storage {
			if !desired[name] {
				_ = app.StorageEmpty(ctx, app.StorageEmptyRequest{
					Cluster: app.Cluster{AppName: dc.Cluster.AppName, Env: dc.Cluster.Env, Output: dc.Cluster.Output},
					Storage: dc.Storage, Name: name,
				})
				_ = app.StorageDelete(ctx, app.StorageDeleteRequest{Cluster: dc.Cluster, Storage: dc.Storage, Name: name})
			}
		}
	}
	return nil
}
