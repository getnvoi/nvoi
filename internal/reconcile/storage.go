package reconcile

import (
	"context"

	"github.com/getnvoi/nvoi/internal/config"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// Storage creates buckets and returns the merged credentials map.
//
// No orphan cleanup here — storage buckets hold user data, and the only
// safe deletion path is the explicit `bin/destroy --delete-storage`
// flag (which goes through internal/core/teardown.go, not reconcile).
// Pre-D3 this step had an "orphan" loop that compared `live.Storage`
// against cfg, but `live.Storage` was always derived from `cfg` itself
// (DescribeLive seeded it from req.StorageNames) — the loop was
// vacuous. Removed in D3 with the rest of DescribeLive.
func Storage(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig) (map[string]string, error) {
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
	return allCreds, nil
}
