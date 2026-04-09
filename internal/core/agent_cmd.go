// Package core is the direct CLI backend — resolves providers from flags and env vars, calls pkg/core functions.
package core

import (
	"context"

	"github.com/getnvoi/nvoi/internal/commands"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/managed"
)

func (d *DirectBackend) AgentSet(ctx context.Context, name string, opts commands.ManagedOpts) error {
	env, err := d.readSecretsFromCluster(ctx, opts.Secrets)
	if err != nil {
		return err
	}
	result, err := managed.Compile(managed.Request{
		Kind:    opts.Kind,
		Name:    name,
		Env:     env,
		Context: managed.Context{DefaultVolumeServer: "master"},
	})
	if err != nil {
		return err
	}
	return d.execBundle(ctx, result.Bundle)
}

func (d *DirectBackend) AgentDelete(ctx context.Context, name, kind string) error {
	return d.deleteByShape(ctx, kind, name)
}

func (d *DirectBackend) AgentList(ctx context.Context) error {
	return d.listManaged(ctx, "agent")
}

func (d *DirectBackend) AgentExec(ctx context.Context, name, kind string, command []string) error {
	if err := d.verifyManagedKind(ctx, name, kind); err != nil {
		return err
	}
	return app.Exec(ctx, app.ExecRequest{
		Cluster: d.cluster,
		Service: name,
		Command: command,
	})
}

func (d *DirectBackend) AgentLogs(ctx context.Context, name, kind string, opts commands.LogsOpts) error {
	if err := d.verifyManagedKind(ctx, name, kind); err != nil {
		return err
	}
	return app.Logs(ctx, app.LogsRequest{
		Cluster:    d.cluster,
		Service:    name,
		Follow:     opts.Follow,
		Tail:       opts.Tail,
		Since:      opts.Since,
		Previous:   opts.Previous,
		Timestamps: opts.Timestamps,
	})
}
