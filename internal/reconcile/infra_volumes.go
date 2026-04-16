package reconcile

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/internal/config"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/utils"
)

func Volumes(ctx context.Context, dc *config.DeployContext, live *config.LiveState, cfg *config.AppConfig) error {
	connectSSH := dc.ConnectSSH
	if connectSSH == nil {
		connectSSH = sshConnector(dc.Cluster.SSHKey)
	}
	for _, name := range utils.SortedKeys(cfg.Volumes) {
		vol := cfg.Volumes[name]
		if _, err := app.VolumeSet(ctx, app.VolumeSetRequest{
			Cluster: dc.Cluster, Output: dc.Output,
			ConnectSSH: connectSSH,
			Name:       name, Size: vol.Size, Server: vol.Server,
		}); err != nil {
			return err
		}
	}
	if live != nil {
		desired := toSet(utils.SortedKeys(cfg.Volumes))
		for _, name := range live.Volumes {
			if !desired[name] {
				if err := app.VolumeDelete(ctx, app.VolumeDeleteRequest{
					Cluster:    dc.Cluster,
					Output:     dc.Output,
					ConnectSSH: connectSSH,
					Name:       name,
				}); err != nil {
					dc.Log().Warning(fmt.Sprintf("orphan volume %s not removed: %s", name, err))
				}
			}
		}
	}
	return nil
}
