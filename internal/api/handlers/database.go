package handlers

import (
	"context"
	"io"
	"log"
	"strconv"

	"github.com/danielgtaylor/huma/v2"
	pkgcore "github.com/getnvoi/nvoi/pkg/core"
	"gorm.io/gorm"
)

// ── Database ────────────────────────────────────────────────────────────────

type DatabaseBackupListInput struct {
	RepoScopedInput
	Name string `query:"name" required:"true" doc:"Database name"`
}
type DatabaseBackupListOutput struct{ Body []pkgcore.BackupEntry }

type DatabaseBackupDownloadInput struct {
	RepoScopedInput
	Name string `query:"name" required:"true" doc:"Database name"`
	Key  string `path:"key" doc:"Backup key"`
}

type DatabaseSQLInput struct {
	RepoScopedInput
	Body struct {
		Name   string `json:"name" required:"true" doc:"Database name"`
		Engine string `json:"engine" required:"true" doc:"Database engine (postgres or mysql)"`
		Query  string `json:"query" required:"true" doc:"SQL query"`
	}
}
type DatabaseSQLOutput struct {
	Body struct {
		Output string `json:"output"`
	}
}

func DatabaseBackupList(db *gorm.DB) func(context.Context, *DatabaseBackupListInput) (*DatabaseBackupListOutput, error) {
	return func(ctx context.Context, input *DatabaseBackupListInput) (*DatabaseBackupListOutput, error) {
		cluster, err := repoCluster(ctx, db, input.RepoScopedInput)
		if err != nil {
			return nil, err
		}
		entries, err := pkgcore.DatabaseBackupList(ctx, pkgcore.DatabaseBackupListRequest{
			Cluster: *cluster,
			DBName:  input.Name,
		})
		if err != nil {
			return nil, humaError(err)
		}
		return &DatabaseBackupListOutput{Body: entries}, nil
	}
}

func DatabaseBackupDownload(db *gorm.DB) func(context.Context, *DatabaseBackupDownloadInput) (*huma.StreamResponse, error) {
	return func(ctx context.Context, input *DatabaseBackupDownloadInput) (*huma.StreamResponse, error) {
		cluster, err := repoCluster(ctx, db, input.RepoScopedInput)
		if err != nil {
			return nil, err
		}
		body, contentLength, err := pkgcore.DatabaseBackupDownload(ctx, pkgcore.DatabaseBackupDownloadRequest{
			Cluster: *cluster,
			DBName:  input.Name,
			Key:     input.Key,
		})
		if err != nil {
			return nil, humaError(err)
		}
		return &huma.StreamResponse{
			Body: func(ctx huma.Context) {
				ctx.SetHeader("Content-Type", "application/octet-stream")
				if contentLength > 0 {
					ctx.SetHeader("Content-Length", strconv.FormatInt(contentLength, 10))
				}
				defer body.Close()
				if _, err := io.Copy(ctx.BodyWriter(), body); err != nil {
					log.Printf("backup download stream error: %v", err)
				}
			},
		}, nil
	}
}

func DatabaseSQL(db *gorm.DB) func(context.Context, *DatabaseSQLInput) (*DatabaseSQLOutput, error) {
	return func(ctx context.Context, input *DatabaseSQLInput) (*DatabaseSQLOutput, error) {
		cluster, err := repoCluster(ctx, db, input.RepoScopedInput)
		if err != nil {
			return nil, err
		}
		out, err := pkgcore.DatabaseSQL(ctx, pkgcore.DatabaseSQLRequest{
			Cluster: *cluster,
			DBName:  input.Body.Name,
			Engine:  input.Body.Engine,
			Query:   input.Body.Query,
		})
		if err != nil {
			return nil, humaError(err)
		}
		return &DatabaseSQLOutput{Body: struct {
			Output string `json:"output"`
		}{Output: out}}, nil
	}
}
