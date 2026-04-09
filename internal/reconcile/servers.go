package reconcile

import (
	"context"

	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/utils"
)

func Servers(ctx context.Context, dc *DeployContext, live *LiveState, cfg *AppConfig) error {
	masters, workers := SplitServers(cfg.Servers)
	for _, s := range masters {
		creds := dc.Cluster.Credentials
		if s.Region != "" {
			creds = copyMap(creds)
			creds["region"] = s.Region
		}
		if _, err := app.ComputeSet(ctx, app.ComputeSetRequest{
			Cluster: clusterWith(dc, creds), Name: s.Name,
			ServerType: s.Type, Region: s.Region, Worker: false,
		}); err != nil {
			return err
		}
	}
	for _, s := range workers {
		creds := dc.Cluster.Credentials
		if s.Region != "" {
			creds = copyMap(creds)
			creds["region"] = s.Region
		}
		if _, err := app.ComputeSet(ctx, app.ComputeSetRequest{
			Cluster: clusterWith(dc, creds), Name: s.Name,
			ServerType: s.Type, Region: s.Region, Worker: true,
		}); err != nil {
			return err
		}
	}

	if live != nil {
		desired := toSet(utils.SortedKeys(cfg.Servers))
		for _, name := range live.Servers {
			if !desired[name] {
				drainNode(ctx, dc, name)
				_ = app.ComputeDelete(ctx, app.ComputeDeleteRequest{Cluster: dc.Cluster, Name: name})
			}
		}
	}
	return nil
}
