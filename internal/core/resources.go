package core

import (
	"context"
	"encoding/json"
	"os"

	"github.com/getnvoi/nvoi/internal/render"
	app "github.com/getnvoi/nvoi/pkg/core"
)

func (d *DirectBackend) Resources(ctx context.Context, jsonOutput bool) error {
	groups, err := app.Resources(ctx, app.ResourcesRequest{
		Compute: app.ProviderRef{Name: d.cluster.Provider, Creds: d.cluster.Credentials},
		DNS:     d.dns,
		Storage: d.storage,
	})
	if err != nil {
		return err
	}
	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(groups)
	}
	render.RenderResources(groups)
	return nil
}
