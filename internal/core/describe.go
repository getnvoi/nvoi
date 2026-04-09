package core

import (
	"context"
	"encoding/json"
	"os"

	"github.com/getnvoi/nvoi/internal/render"
	app "github.com/getnvoi/nvoi/pkg/core"
)

func (d *DirectBackend) Describe(ctx context.Context, jsonOutput bool) error {
	req := app.DescribeRequest{Cluster: d.cluster}
	if jsonOutput {
		raw, err := app.DescribeJSON(ctx, req)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(raw)
	}
	res, err := app.Describe(ctx, req)
	if err != nil {
		return err
	}
	render.RenderDescribe(res)
	return nil
}
