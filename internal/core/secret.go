package core

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/internal/render"
	app "github.com/getnvoi/nvoi/pkg/core"
)

func (d *DirectBackend) SecretSet(ctx context.Context, key, value string) error {
	return app.SecretSet(ctx, app.SecretSetRequest{
		Cluster: d.cluster,
		Key:     key,
		Value:   value,
	})
}

func (d *DirectBackend) SecretDelete(ctx context.Context, key string) error {
	return d.handleDelete(app.SecretDelete(ctx, app.SecretDeleteRequest{
		Cluster: d.cluster,
		Key:     key,
	}))
}

func (d *DirectBackend) SecretList(ctx context.Context) error {
	keys, err := app.SecretList(ctx, app.SecretListRequest{Cluster: d.cluster})
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		fmt.Println("no secrets")
		return nil
	}
	t := render.NewTable("KEY")
	for _, k := range keys {
		t.Row(k)
	}
	t.Print()
	return nil
}

func (d *DirectBackend) SecretReveal(ctx context.Context, key string) (string, error) {
	return app.SecretReveal(ctx, app.SecretRevealRequest{
		Cluster: d.cluster,
		Key:     key,
	})
}
