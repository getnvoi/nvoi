package core

import (
	"context"

	"github.com/getnvoi/nvoi/internal/commands"
	app "github.com/getnvoi/nvoi/pkg/core"
)

func (d *DirectBackend) ServiceSet(ctx context.Context, name string, opts commands.ServiceOpts) error {
	if err := app.ServiceSet(ctx, app.ServiceSetRequest{
		Cluster:    d.cluster,
		Name:       name,
		Image:      opts.Image,
		Port:       opts.Port,
		Command:    opts.Command,
		Replicas:   opts.Replicas,
		EnvVars:    opts.Env,
		Secrets:    opts.Secrets,
		Storages:   opts.Storage,
		Volumes:    opts.Volumes,
		HealthPath: opts.Health,
		Server:     opts.Server,
	}); err != nil {
		return err
	}
	if opts.NoWait {
		return nil
	}
	kind := "deployment"
	if len(opts.Volumes) > 0 {
		kind = "statefulset"
	}
	return app.WaitRollout(ctx, app.WaitRolloutRequest{
		Cluster:        d.cluster,
		Service:        name,
		WorkloadKind:   kind,
		HasHealthCheck: opts.Health != "",
	})
}

func (d *DirectBackend) ServiceDelete(ctx context.Context, name string) error {
	return d.handleDelete(app.ServiceDelete(ctx, app.ServiceDeleteRequest{
		Cluster: d.cluster,
		Name:    name,
	}))
}
