package core

import (
	"context"

	"github.com/getnvoi/nvoi/internal/render"
	app "github.com/getnvoi/nvoi/pkg/core"
)

func (d *DirectBackend) InstanceSet(ctx context.Context, name, serverType, region, role string) error {
	creds := d.cluster.Credentials
	if region != "" {
		creds = copyMap(creds)
		creds["region"] = region
	}
	_, err := app.ComputeSet(ctx, app.ComputeSetRequest{
		Cluster:    d.clusterWith(creds),
		Name:       name,
		ServerType: serverType,
		Region:     region,
		Worker:     role == "worker",
	})
	return err
}

func (d *DirectBackend) InstanceDelete(ctx context.Context, name string) error {
	return d.handleDelete(app.ComputeDelete(ctx, app.ComputeDeleteRequest{
		Cluster: d.cluster,
		Name:    name,
	}))
}

func (d *DirectBackend) InstanceList(ctx context.Context) error {
	servers, err := app.ComputeList(ctx, app.ComputeListRequest{Cluster: d.cluster})
	if err != nil {
		return err
	}
	t := render.NewTable("NAME", "STATUS", "IPv4", "PRIVATE IP")
	for _, s := range servers {
		t.Row(s.Name, string(s.Status), s.IPv4, s.PrivateIP)
	}
	t.Print()
	return nil
}
