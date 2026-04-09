package core

import (
	"context"

	app "github.com/getnvoi/nvoi/pkg/core"
)

func (d *DirectBackend) Exec(ctx context.Context, service string, command []string) error {
	return app.Exec(ctx, app.ExecRequest{
		Cluster: d.cluster,
		Service: service,
		Command: command,
	})
}
