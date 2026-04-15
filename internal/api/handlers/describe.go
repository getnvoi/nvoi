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
		cluster, cfgNames, err := repoClusterWithConfig(ctx, db, input.RepoScopedInput)
		if err != nil {
			return nil, err
		}

		res, err := pkgcore.Describe(ctx, pkgcore.DescribeRequest{
			Cluster:        *cluster,
			StorageNames:   cfgNames.StorageNames,
			ServiceSecrets: cfgNames.ServiceSecrets,
		})
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
				Creds: resolveRepoCreds(ctx, repo, "compute", repo.ComputeProvider),
			}
		}
		if repo.DNSProvider != nil {
			req.DNS = pkgcore.ProviderRef{
				Name:  repo.DNSProvider.Provider,
				Creds: resolveRepoCreds(ctx, repo, "dns", repo.DNSProvider),
			}
		}
		if repo.StorageProvider != nil {
			req.Storage = pkgcore.ProviderRef{
				Name:  repo.StorageProvider.Provider,
				Creds: resolveRepoCreds(ctx, repo, "storage", repo.StorageProvider),
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

// repoConfigNames resolves config-derived names (storage, secrets) from a repo.
type repoConfigNames struct {
	StorageNames   []string
	ServiceSecrets map[string][]string
}

func repoClusterWithConfig(ctx context.Context, db *gorm.DB, input RepoScopedInput) (*pkgcore.Cluster, repoConfigNames, error) {
	user := api.UserFromContext(ctx)
	repo, err := findRepo(db, user.ID, input.WorkspaceID, input.RepoID)
	if err != nil {
		return nil, repoConfigNames{}, err
	}
	cluster := clusterFromRepo(ctx, repo)
	var names repoConfigNames
	if repo.Config != "" {
		if cfg, err := config.ParseAppConfig([]byte(repo.Config)); err == nil {
			if err := cfg.Resolve(); err == nil {
				names.StorageNames = cfg.StorageNames()
			}
			names.ServiceSecrets = cfg.ServiceSecrets()
		}
	}
	return cluster, names, nil
}

// credentialSourceFromRepo returns the single CredentialSource for a repo.
// If the repo has a linked secrets provider, credentials are fetched transiently.
// Otherwise, credentials come from the InfraProvider's encrypted DB map.
func credentialSourceFromRepo(ctx context.Context, repo *api.Repo, infraProv *api.InfraProvider) provider.CredentialSource {
	if repo.SecretsProvider != nil {
		spCreds := repo.SecretsProvider.CredentialsMap()
		if sp, err := provider.ResolveSecrets(repo.SecretsProvider.Provider, spCreds); err == nil {
			return provider.SecretsSource{Ctx: ctx, Provider: sp}
		}
	}
	if infraProv != nil {
		return provider.MapSource{M: infraProv.CredentialsMap()}
	}
	return provider.MapSource{M: nil}
}

// resolveRepoCreds resolves credentials for an infra provider using the repo's credential source.
func resolveRepoCreds(ctx context.Context, repo *api.Repo, kind string, infraProv *api.InfraProvider) map[string]string {
	if infraProv == nil {
		return nil
	}
	source := credentialSourceFromRepo(ctx, repo, infraProv)
	schema, err := provider.GetSchema(kind, infraProv.Provider)
	if err != nil {
		return infraProv.CredentialsMap() // fallback
	}
	creds, err := provider.ResolveFrom(schema, source)
	if err != nil {
		return infraProv.CredentialsMap() // fallback
	}
	return creds
}

// clusterFromRepo builds a Cluster from Repo's InfraProvider links. No RepoConfig needed.
func clusterFromRepo(ctx context.Context, repo *api.Repo) *pkgcore.Cluster {
	computeName := ""
	var computeCreds map[string]string
	if repo.ComputeProvider != nil {
		computeName = repo.ComputeProvider.Provider
		computeCreds = resolveRepoCreds(ctx, repo, "compute", repo.ComputeProvider)
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
	return clusterFromRepo(ctx, repo), nil
}
