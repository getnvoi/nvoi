package handlers

import (
	"context"

	"github.com/getnvoi/nvoi/internal/api"
	"github.com/getnvoi/nvoi/internal/config"
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
		cluster, storageNames, err := repoClusterWithStorage(ctx, db, input.RepoScopedInput)
		if err != nil {
			return nil, err
		}

		res, err := pkgcore.Describe(ctx, pkgcore.DescribeRequest{Cluster: *cluster, StorageNames: storageNames})
		if err != nil {
			return nil, humaError(err)
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

		req := pkgcore.ResourcesRequest{}
		if repo.ComputeProvider != nil {
			req.Compute = pkgcore.ProviderRef{
				Name:  repo.ComputeProvider.Provider,
				Creds: repo.ComputeProvider.CredentialsMap(),
			}
		}
		if repo.DNSProvider != nil {
			req.DNS = pkgcore.ProviderRef{
				Name:  repo.DNSProvider.Provider,
				Creds: repo.DNSProvider.CredentialsMap(),
			}
		}
		if repo.StorageProvider != nil {
			req.Storage = pkgcore.ProviderRef{
				Name:  repo.StorageProvider.Provider,
				Creds: repo.StorageProvider.CredentialsMap(),
			}
		}

		groups, err := pkgcore.Resources(ctx, req)
		if err != nil {
			return nil, humaError(err)
		}

		return &ListResourcesOutput{Body: groups}, nil
	}
}

// ── Helpers ──────────────────────────────────────────────────────────────────

// repoClusterWithStorage resolves a cluster and the config-derived storage names from a repo.
func repoClusterWithStorage(ctx context.Context, db *gorm.DB, input RepoScopedInput) (*pkgcore.Cluster, []string, error) {
	user := api.UserFromContext(ctx)
	repo, err := findRepo(db, user.ID, input.WorkspaceID, input.RepoID)
	if err != nil {
		return nil, nil, err
	}
	cluster := clusterFromRepo(repo)
	var storageNames []string
	if repo.Config != "" {
		if cfg, err := config.ParseAppConfig([]byte(repo.Config)); err == nil {
			if err := cfg.Resolve(); err == nil {
				storageNames = cfg.StorageNames()
			}
		}
	}
	return cluster, storageNames, nil
}

// clusterFromRepo builds a Cluster from Repo's InfraProvider links. No RepoConfig needed.
func clusterFromRepo(repo *api.Repo) *pkgcore.Cluster {
	computeName, computeCreds := "", map[string]string(nil)
	if repo.ComputeProvider != nil {
		computeName = repo.ComputeProvider.Provider
		computeCreds = repo.ComputeProvider.CredentialsMap()
	}
	return &pkgcore.Cluster{
		AppName:     repo.Name,
		Env:         repo.Environment,
		Provider:    computeName,
		Credentials: computeCreds,
		SSHKey:      []byte(repo.SSHPrivateKey),
	}
}

// repoCluster resolves user -> repo -> cluster from a RepoScopedInput.
func repoCluster(ctx context.Context, db *gorm.DB, input RepoScopedInput) (*pkgcore.Cluster, error) {
	user := api.UserFromContext(ctx)
	repo, err := findRepo(db, user.ID, input.WorkspaceID, input.RepoID)
	if err != nil {
		return nil, err
	}
	return clusterFromRepo(repo), nil
}
