package handlers

import (
	"context"

	pkgcore "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/provider"
	"gorm.io/gorm"
)

type ListVolumesInput struct{ RepoScopedInput }
type ListVolumesOutput struct{ Body []*provider.Volume }

func ListVolumes(db *gorm.DB) func(context.Context, *ListVolumesInput) (*ListVolumesOutput, error) {
	return func(ctx context.Context, input *ListVolumesInput) (*ListVolumesOutput, error) {
		cluster, err := repoCluster(ctx, db, input.RepoScopedInput)
		if err != nil {
			return nil, err
		}
		volumes, err := pkgcore.VolumeList(ctx, pkgcore.VolumeListRequest{Cluster: *cluster})
		if err != nil {
			return nil, humaError(err)
		}
		return &ListVolumesOutput{Body: volumes}, nil
	}
}
