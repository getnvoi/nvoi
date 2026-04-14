package handlers

import (
	"context"

	pkgcore "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/provider"
	"gorm.io/gorm"
)

type ListInstancesInput struct{ RepoScopedInput }
type ListInstancesOutput struct{ Body []*provider.Server }

func ListInstances(db *gorm.DB) func(context.Context, *ListInstancesInput) (*ListInstancesOutput, error) {
	return func(ctx context.Context, input *ListInstancesInput) (*ListInstancesOutput, error) {
		cluster, err := repoCluster(ctx, db, input.RepoScopedInput)
		if err != nil {
			return nil, err
		}
		servers, err := pkgcore.ComputeList(ctx, pkgcore.ComputeListRequest{Cluster: *cluster})
		if err != nil {
			return nil, humaError(err)
		}
		return &ListInstancesOutput{Body: servers}, nil
	}
}
