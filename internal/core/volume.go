package core

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/internal/render"
	app "github.com/getnvoi/nvoi/pkg/core"
)

func (d *DirectBackend) VolumeSet(ctx context.Context, name string, size int, server string) error {
	_, err := app.VolumeSet(ctx, app.VolumeSetRequest{
		Cluster: d.cluster,
		Name:    name,
		Size:    size,
		Server:  server,
	})
	return err
}

func (d *DirectBackend) VolumeDelete(ctx context.Context, name string) error {
	return d.handleDelete(app.VolumeDelete(ctx, app.VolumeDeleteRequest{
		Cluster: d.cluster,
		Name:    name,
	}))
}

func (d *DirectBackend) VolumeList(ctx context.Context) error {
	volumes, err := app.VolumeList(ctx, app.VolumeListRequest{Cluster: d.cluster})
	if err != nil {
		return err
	}
	t := render.NewTable("NAME", "SIZE", "SERVER", "DEVICE")
	for _, v := range volumes {
		t.Row(v.Name, fmt.Sprintf("%dGB", v.Size), v.ServerName, v.DevicePath)
	}
	t.Print()
	return nil
}
