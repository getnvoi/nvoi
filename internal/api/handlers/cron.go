package handlers

import (
	"context"

	"github.com/danielgtaylor/huma/v2"
	"github.com/getnvoi/nvoi/internal/api"
	pkgcore "github.com/getnvoi/nvoi/pkg/core"
	"gorm.io/gorm"
)

type CronRunInput struct {
	RepoScopedInput
	Name string `path:"name" doc:"Cron job name"`
}

func CronRun(db *gorm.DB) func(context.Context, *CronRunInput) (*huma.StreamResponse, error) {
	return func(ctx context.Context, input *CronRunInput) (*huma.StreamResponse, error) {
		user := api.UserFromContext(ctx)
		repo, err := findRepo(db, user.ID, input.WorkspaceID, input.RepoID)
		if err != nil {
			return nil, err
		}
		cluster := clusterFromRepo(ctx, repo)

		return streamOperation(func(out pkgcore.Output) error {
			cluster.Output = out
			return pkgcore.CronRun(ctx, pkgcore.CronRunRequest{
				Cluster: *cluster,
				Name:    input.Name,
			})
		}), nil
	}
}
