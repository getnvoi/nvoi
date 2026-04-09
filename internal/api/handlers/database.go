package handlers

import (
	"context"

	"github.com/danielgtaylor/huma/v2"
	pkgcore "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/managed"
	"gorm.io/gorm"
)

// ── Input / Output types ─────────────────────────────────────────────────────

type DatabaseListInput struct{ RepoScopedInput }
type DatabaseListOutput struct{ Body []pkgcore.ManagedService }

type BackupCreateInput struct {
	RepoScopedInput
	Name string `path:"name" doc:"Database service name"`
}
type BackupCreateOutput struct {
	Body struct {
		Status string `json:"status"`
	}
}

type BackupListInput struct {
	RepoScopedInput
	Name string `path:"name" doc:"Database service name"`
}
type BackupListOutput struct{ Body []pkgcore.BackupArtifact }

type BackupDownloadInput struct {
	RepoScopedInput
	Name string `path:"name" doc:"Database service name"`
	Key  string `path:"key" doc:"Backup artifact key"`
}

// ── Handlers ─────────────────────────────────────────────────────────────────

func DatabaseList(db *gorm.DB) func(context.Context, *DatabaseListInput) (*DatabaseListOutput, error) {
	return func(ctx context.Context, input *DatabaseListInput) (*DatabaseListOutput, error) {
		cluster, err := repoCluster(ctx, db, input.RepoScopedInput)
		if err != nil {
			return nil, err
		}

		var all []pkgcore.ManagedService
		for _, kind := range managed.KindsForCategory("database") {
			services, err := pkgcore.ManagedList(ctx, pkgcore.ManagedListRequest{
				Cluster: *cluster,
				Kind:    kind,
			})
			if err != nil {
				return nil, huma.Error500InternalServerError(err.Error())
			}
			all = append(all, services...)
		}

		return &DatabaseListOutput{Body: all}, nil
	}
}

func BackupCreate(db *gorm.DB) func(context.Context, *BackupCreateInput) (*BackupCreateOutput, error) {
	return func(ctx context.Context, input *BackupCreateInput) (*BackupCreateOutput, error) {
		cluster, err := repoCluster(ctx, db, input.RepoScopedInput)
		if err != nil {
			return nil, err
		}

		cronName := input.Name + "-backup"
		if err := pkgcore.BackupCreate(ctx, pkgcore.BackupCreateRequest{
			Cluster:  *cluster,
			CronName: cronName,
		}); err != nil {
			return nil, huma.Error500InternalServerError(err.Error())
		}

		return &BackupCreateOutput{Body: struct {
			Status string `json:"status"`
		}{Status: "completed"}}, nil
	}
}

func BackupList(db *gorm.DB) func(context.Context, *BackupListInput) (*BackupListOutput, error) {
	return func(ctx context.Context, input *BackupListInput) (*BackupListOutput, error) {
		cluster, err := repoCluster(ctx, db, input.RepoScopedInput)
		if err != nil {
			return nil, err
		}

		storageName := input.Name + "-backups"
		artifacts, err := pkgcore.BackupList(ctx, pkgcore.BackupListRequest{
			Cluster: *cluster,
			Name:    storageName,
		})
		if err != nil {
			return nil, huma.Error500InternalServerError(err.Error())
		}

		return &BackupListOutput{Body: artifacts}, nil
	}
}

func BackupDownload(db *gorm.DB) func(context.Context, *BackupDownloadInput) (*huma.StreamResponse, error) {
	return func(ctx context.Context, input *BackupDownloadInput) (*huma.StreamResponse, error) {
		cluster, err := repoCluster(ctx, db, input.RepoScopedInput)
		if err != nil {
			return nil, err
		}

		storageName := input.Name + "-backups"
		req := pkgcore.BackupDownloadRequest{
			Cluster: *cluster,
			Name:    storageName,
			Key:     input.Key,
		}

		return &huma.StreamResponse{
			Body: func(ctx huma.Context) {
				ctx.SetHeader("Content-Type", "application/octet-stream")
				ctx.SetHeader("Content-Disposition", "attachment; filename=\""+input.Key+"\"")
				// Stream directly from S3 to the HTTP response — no buffering.
				if err := pkgcore.BackupDownload(context.Background(), req, ctx.BodyWriter()); err != nil {
					// Headers already sent — can't change status code. Log the error.
					ctx.BodyWriter().Write([]byte("\nerror: " + err.Error()))
				}
			},
		}, nil
	}
}
