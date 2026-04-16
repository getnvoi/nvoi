package reconcile

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/internal/config"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/utils"
)

func Storage(ctx context.Context, dc *config.DeployContext, live *config.LiveState, cfg *config.AppConfig) (map[string]string, error) {
	allCreds := map[string]string{}

	for _, name := range utils.SortedKeys(cfg.Storage) {
		st := cfg.Storage[name]
		creds, err := app.StorageSet(ctx, app.StorageSetRequest{
			Cluster: dc.Cluster, Storage: dc.Storage,
			Name: name, Bucket: st.Bucket, CORS: st.CORS, ExpireDays: st.ExpireDays,
		})
		if err != nil {
			return nil, err
		}
		for k, v := range creds {
			allCreds[k] = v
		}
	}

	if live != nil {
		desired := toSet(utils.SortedKeys(cfg.Storage))
		protected := map[string]bool{}
		for _, db := range cfg.Database {
			protected[db.BackupBucket] = true
		}
		for _, name := range live.Storage {
			if !desired[name] && !protected[name] {
				if err := app.StorageEmpty(ctx, app.StorageEmptyRequest{
					Cluster: app.Cluster{AppName: dc.Cluster.AppName, Env: dc.Cluster.Env, Output: dc.Cluster.Output},
					Storage: dc.Storage, Name: name,
				}); err != nil {
					dc.Cluster.Log().Warning(fmt.Sprintf("orphan storage %s not emptied: %s", name, err))
				}
				if err := app.StorageDelete(ctx, app.StorageDeleteRequest{Cluster: dc.Cluster, Storage: dc.Storage, Name: name}); err != nil {
					dc.Cluster.Log().Warning(fmt.Sprintf("orphan storage %s not removed: %s", name, err))
				}
			}
		}
	}
	return allCreds, nil
}
