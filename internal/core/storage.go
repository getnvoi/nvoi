package core

import (
	"context"
	"fmt"

	app "github.com/getnvoi/nvoi/pkg/core"
)

func (d *DirectBackend) StorageSet(ctx context.Context, name, bucket string, cors bool, expireDays int) error {
	return app.StorageSet(ctx, app.StorageSetRequest{
		Cluster:    d.cluster,
		Storage:    d.storage,
		Name:       name,
		Bucket:     bucket,
		CORS:       cors,
		ExpireDays: expireDays,
	})
}

func (d *DirectBackend) StorageDelete(ctx context.Context, name string) error {
	return d.handleDelete(app.StorageDelete(ctx, app.StorageDeleteRequest{
		Cluster: d.cluster,
		Storage: d.storage,
		Name:    name,
	}))
}

func (d *DirectBackend) StorageEmpty(ctx context.Context, name string) error {
	return d.handleDelete(app.StorageEmpty(ctx, app.StorageEmptyRequest{
		Cluster: app.Cluster{
			AppName: d.cluster.AppName,
			Env:     d.cluster.Env,
			Output:  d.cluster.Output,
		},
		Storage: d.storage,
		Name:    name,
	}))
}

func (d *DirectBackend) StorageList(ctx context.Context) error {
	items, err := app.StorageList(ctx, app.StorageListRequest{Cluster: d.cluster})
	if err != nil {
		return err
	}
	if len(items) == 0 {
		fmt.Println("no storage configured")
		return nil
	}
	for _, item := range items {
		fmt.Printf("%-20s %s\n", item.Name, item.Bucket)
	}
	return nil
}
