package reconcile

import (
	"context"

	"github.com/getnvoi/nvoi/internal/config"
	app "github.com/getnvoi/nvoi/pkg/core"
)

func Build(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig) error {
	if len(cfg.Build) == 0 {
		return nil
	}
	targets, err := app.ParseBuildTargets(buildTargetStrings(cfg.Build))
	if err != nil {
		return err
	}
	if len(targets) == 1 {
		_, err = app.BuildRun(ctx, app.BuildRunRequest{
			Cluster: dc.Cluster, Builder: dc.Builder, BuilderCredentials: dc.BuildCreds,
			Source: targets[0].Source, Name: targets[0].Name,
			GitUsername: dc.GitUsername, GitToken: dc.GitToken,
		})
		return err
	}
	return app.BuildParallel(ctx, app.BuildParallelRequest{
		Cluster: dc.Cluster, Builder: dc.Builder, BuilderCredentials: dc.BuildCreds,
		Targets: targets, GitUsername: dc.GitUsername, GitToken: dc.GitToken,
	})
}
