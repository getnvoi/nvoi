package core

import (
	"context"

	app "github.com/getnvoi/nvoi/pkg/core"
)

func (d *DirectBackend) SSH(ctx context.Context, command []string) error {
	return app.SSH(ctx, app.SSHRequest{
		Cluster: d.cluster,
		Command: command,
	})
}
