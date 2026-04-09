package cli

import (
	"context"
	"encoding/json"
	"os"

	"github.com/getnvoi/nvoi/internal/render"
	pkgcore "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/provider"
)

func (c *CloudBackend) Describe(ctx context.Context, jsonOutput bool) error {
	var res pkgcore.DescribeResult
	if err := c.client.Do("GET", c.repoPath("/describe"), nil, &res); err != nil {
		return err
	}
	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}
	render.RenderDescribe(&res)
	return nil
}

func (c *CloudBackend) Resources(ctx context.Context, jsonOutput bool) error {
	var groups []provider.ResourceGroup
	if err := c.client.Do("GET", c.repoPath("/resources"), nil, &groups); err != nil {
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
