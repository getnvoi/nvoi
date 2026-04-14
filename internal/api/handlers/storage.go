package handlers

import (
	"context"

	"github.com/danielgtaylor/huma/v2"
	"github.com/getnvoi/nvoi/internal/api"
	pkgcore "github.com/getnvoi/nvoi/pkg/core"
	"gorm.io/gorm"
)

type ListStorageInput struct{ RepoScopedInput }
type ListStorageOutput struct{ Body []pkgcore.StorageItem }

type EmptyStorageInput struct {
	RepoScopedInput
	Name string `path:"name" doc:"Storage name"`
}
type EmptyStorageOutput struct {
	Body struct {
		Status string `json:"status"`
	}
}

func ListStorageBuckets(db *gorm.DB) func(context.Context, *ListStorageInput) (*ListStorageOutput, error) {
	return func(ctx context.Context, input *ListStorageInput) (*ListStorageOutput, error) {
		cluster, storageNames, err := repoClusterWithStorage(ctx, db, input.RepoScopedInput)
		if err != nil {
			return nil, err
		}
		items, err := pkgcore.StorageList(ctx, pkgcore.StorageListRequest{Cluster: *cluster, StorageNames: storageNames})
		if err != nil {
			return nil, humaError(err)
		}
		return &ListStorageOutput{Body: items}, nil
	}
}

func EmptyStorage(db *gorm.DB) func(context.Context, *EmptyStorageInput) (*EmptyStorageOutput, error) {
	return func(ctx context.Context, input *EmptyStorageInput) (*EmptyStorageOutput, error) {
		user := api.UserFromContext(ctx)
		repo, err := findRepo(db, user.ID, input.WorkspaceID, input.RepoID)
		if err != nil {
			return nil, err
		}

		if repo.StorageProvider == nil {
			return nil, huma.Error400BadRequest("no storage provider configured")
		}

		cluster := clusterFromRepo(repo)
		err = pkgcore.StorageEmpty(ctx, pkgcore.StorageEmptyRequest{
			Cluster: *cluster,
			Storage: pkgcore.ProviderRef{
				Name:  repo.StorageProvider.Provider,
				Creds: repo.StorageProvider.CredentialsMap(),
			},
			Name: input.Name,
		})
		if err != nil {
			return nil, humaError(err)
		}

		return &EmptyStorageOutput{Body: struct {
			Status string `json:"status"`
		}{Status: "emptied"}}, nil
	}
}
