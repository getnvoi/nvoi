package core

import (
	"context"

	"github.com/getnvoi/nvoi/internal/commands"
	app "github.com/getnvoi/nvoi/pkg/core"
)

func (d *DirectBackend) Logs(ctx context.Context, service string, opts commands.LogsOpts) error {
	return app.Logs(ctx, app.LogsRequest{
		Cluster:    d.cluster,
		Service:    service,
		Follow:     opts.Follow,
		Tail:       opts.Tail,
		Since:      opts.Since,
		Previous:   opts.Previous,
		Timestamps: opts.Timestamps,
	})
}
