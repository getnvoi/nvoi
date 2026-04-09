package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/internal/commands"
	"github.com/getnvoi/nvoi/internal/render"
	app "github.com/getnvoi/nvoi/pkg/core"
)

func (d *DirectBackend) Build(ctx context.Context, opts commands.BuildOpts) error {
	parsed, err := app.ParseBuildTargets(opts.Targets)
	if err != nil {
		return err
	}

	platform := opts.Platform
	if opts.Architecture != "" {
		switch opts.Architecture {
		case "amd64", "amd":
			platform = "linux/amd64"
		case "arm64", "arm":
			platform = "linux/arm64"
		default:
			return fmt.Errorf("invalid architecture %q — use amd64 or arm64", opts.Architecture)
		}
	}

	if len(parsed) == 1 {
		_, err = app.BuildRun(ctx, app.BuildRunRequest{
			Cluster:            d.cluster,
			Builder:            d.builder,
			BuilderCredentials: d.builderCreds,
			Source:             parsed[0].Source,
			Name:               parsed[0].Name,
			Branch:             opts.Branch,
			Platform:           platform,
			GitUsername:        d.gitUsername,
			GitToken:           d.gitToken,
			History:            opts.History,
		})
		return err
	}

	return app.BuildParallel(ctx, app.BuildParallelRequest{
		Cluster:            d.cluster,
		Builder:            d.builder,
		BuilderCredentials: d.builderCreds,
		Targets:            parsed,
		Platform:           platform,
		GitUsername:        d.gitUsername,
		GitToken:           d.gitToken,
	})
}

func (d *DirectBackend) BuildList(ctx context.Context) error {
	images, err := app.BuildList(ctx, app.BuildListRequest{Cluster: d.cluster})
	if err != nil {
		return err
	}
	if len(images) == 0 {
		fmt.Println("no images in registry")
		return nil
	}
	t := render.NewTable("IMAGE", "TAGS")
	for _, img := range images {
		t.Row(img.Name, strings.Join(img.Tags, ", "))
	}
	t.Print()
	return nil
}

func (d *DirectBackend) BuildLatest(ctx context.Context, name string) (string, error) {
	return app.BuildLatest(ctx, app.BuildLatestRequest{
		Cluster: d.cluster,
		Name:    name,
	})
}

func (d *DirectBackend) BuildPrune(ctx context.Context, name string, keep int) error {
	return app.BuildPrune(ctx, app.BuildPruneRequest{
		Cluster: d.cluster,
		Name:    name,
		Keep:    keep,
	})
}
