package handlers

import (
	"context"
	"fmt"

	"github.com/danielgtaylor/huma/v2"
	"github.com/getnvoi/nvoi/internal/api"
	"github.com/getnvoi/nvoi/internal/api/config"
	pkgcore "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/provider"
	"gorm.io/gorm"
)

// ── Input / Output types ─────────────────────────────────────────────────────

type DescribeClusterInput struct {
	RepoScopedInput
}

type DescribeClusterOutput struct {
	Body pkgcore.DescribeResult
}

type ListResourcesInput struct {
	RepoScopedInput
}

type ListResourcesOutput struct {
	Body []provider.ResourceGroup
}

// ── Handlers ─────────────────────────────────────────────────────────────────

func DescribeCluster(db *gorm.DB) func(context.Context, *DescribeClusterInput) (*DescribeClusterOutput, error) {
	return func(ctx context.Context, input *DescribeClusterInput) (*DescribeClusterOutput, error) {
		user := api.UserFromContext(ctx)
		repo, err := findRepo(db, user.ID, input.WorkspaceID, input.RepoID)
		if err != nil {
			return nil, err
		}

		cluster, err := clusterFromLatestConfig(db, repo)
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}

		res, err := pkgcore.Describe(ctx, pkgcore.DescribeRequest{Cluster: *cluster})
		if err != nil {
			return nil, huma.Error500InternalServerError(err.Error())
		}

		return &DescribeClusterOutput{Body: *res}, nil
	}
}

func ListResources(db *gorm.DB) func(context.Context, *ListResourcesInput) (*ListResourcesOutput, error) {
	return func(ctx context.Context, input *ListResourcesInput) (*ListResourcesOutput, error) {
		user := api.UserFromContext(ctx)
		repo, err := findRepo(db, user.ID, input.WorkspaceID, input.RepoID)
		if err != nil {
			return nil, err
		}

		rc, env, err := latestConfigAndEnv(db, repo)
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}

		creds, err := resolveAllCredentials(rc, env)
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}

		req := pkgcore.ResourcesRequest{
			Compute: pkgcore.ProviderRef{Name: string(rc.ComputeProvider), Creds: creds.Compute},
		}
		if rc.DNSProvider != "" {
			req.DNS = pkgcore.ProviderRef{Name: string(rc.DNSProvider), Creds: creds.DNS}
		}
		if rc.StorageProvider != "" {
			req.Storage = pkgcore.ProviderRef{Name: string(rc.StorageProvider), Creds: creds.Storage}
		}

		groups, err := pkgcore.Resources(ctx, req)
		if err != nil {
			return nil, huma.Error500InternalServerError(err.Error())
		}

		return &ListResourcesOutput{Body: groups}, nil
	}
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func clusterFromLatestConfig(db *gorm.DB, repo *api.Repo) (*pkgcore.Cluster, error) {
	rc, env, err := latestConfigAndEnv(db, repo)
	if err != nil {
		return nil, err
	}

	creds, err := resolveAllCredentials(rc, env)
	if err != nil {
		return nil, err
	}

	return &pkgcore.Cluster{
		AppName:     repo.Name,
		Env:         repo.Environment,
		Provider:    string(rc.ComputeProvider),
		Credentials: creds.Compute,
		SSHKey:      []byte(repo.SSHPrivateKey),
	}, nil
}

func latestConfigAndEnv(db *gorm.DB, repo *api.Repo) (*api.RepoConfig, map[string]string, error) {
	var rc api.RepoConfig
	if err := db.Where("repo_id = ?", repo.ID).Order("version DESC").First(&rc).Error; err != nil {
		return nil, nil, err
	}
	env, err := config.ParseEnv(rc.Env)
	if err != nil {
		return nil, nil, fmt.Errorf("corrupt env: %w", err)
	}
	return &rc, env, nil
}
