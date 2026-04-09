package core

import (
	"context"

	"github.com/getnvoi/nvoi/internal/commands"
	app "github.com/getnvoi/nvoi/pkg/core"
)

func (d *DirectBackend) CronSet(ctx context.Context, name string, opts commands.CronOpts) error {
	return app.CronSet(ctx, app.CronSetRequest{
		Cluster:  d.cluster,
		Name:     name,
		Image:    opts.Image,
		Command:  opts.Command,
		EnvVars:  opts.Env,
		Secrets:  opts.Secrets,
		Storages: opts.Storage,
		Volumes:  opts.Volumes,
		Schedule: opts.Schedule,
		Server:   opts.Server,
	})
}

func (d *DirectBackend) CronDelete(ctx context.Context, name string) error {
	return d.handleDelete(app.CronDelete(ctx, app.CronDeleteRequest{
		Cluster: d.cluster,
		Name:    name,
	}))
}
