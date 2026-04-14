package handlers

import (
	"context"

	pkgcore "github.com/getnvoi/nvoi/pkg/core"
	"gorm.io/gorm"
)

type ListBuildsInput struct{ RepoScopedInput }
type ListBuildsOutput struct{ Body []pkgcore.RegistryImage }

type BuildLatestInput struct {
	RepoScopedInput
	Name string `path:"name" doc:"Build name"`
}
type BuildLatestOutput struct {
	Body struct {
		Image string `json:"image"`
	}
}

type BuildPruneInput struct {
	RepoScopedInput
	Name string `path:"name" doc:"Build name"`
	Body struct {
		Keep int `json:"keep" required:"true" minimum:"1" doc:"Number of tags to keep"`
	}
}
type BuildPruneOutput struct {
	Body struct {
		Status string `json:"status"`
	}
}

func ListBuilds(db *gorm.DB) func(context.Context, *ListBuildsInput) (*ListBuildsOutput, error) {
	return func(ctx context.Context, input *ListBuildsInput) (*ListBuildsOutput, error) {
		cluster, err := repoCluster(ctx, db, input.RepoScopedInput)
		if err != nil {
			return nil, err
		}
		images, err := pkgcore.BuildList(ctx, pkgcore.BuildListRequest{Cluster: *cluster})
		if err != nil {
			return nil, humaError(err)
		}
		return &ListBuildsOutput{Body: images}, nil
	}
}

func BuildLatestImage(db *gorm.DB) func(context.Context, *BuildLatestInput) (*BuildLatestOutput, error) {
	return func(ctx context.Context, input *BuildLatestInput) (*BuildLatestOutput, error) {
		cluster, err := repoCluster(ctx, db, input.RepoScopedInput)
		if err != nil {
			return nil, err
		}
		ref, err := pkgcore.BuildLatest(ctx, pkgcore.BuildLatestRequest{
			Cluster: *cluster,
			Name:    input.Name,
		})
		if err != nil {
			return nil, humaError(err)
		}
		return &BuildLatestOutput{Body: struct {
			Image string `json:"image"`
		}{Image: ref}}, nil
	}
}

func PruneBuild(db *gorm.DB) func(context.Context, *BuildPruneInput) (*BuildPruneOutput, error) {
	return func(ctx context.Context, input *BuildPruneInput) (*BuildPruneOutput, error) {
		cluster, err := repoCluster(ctx, db, input.RepoScopedInput)
		if err != nil {
			return nil, err
		}
		err = pkgcore.BuildPrune(ctx, pkgcore.BuildPruneRequest{
			Cluster: *cluster,
			Name:    input.Name,
			Keep:    input.Body.Keep,
		})
		if err != nil {
			return nil, humaError(err)
		}
		return &BuildPruneOutput{Body: struct {
			Status string `json:"status"`
		}{Status: "pruned"}}, nil
	}
}
