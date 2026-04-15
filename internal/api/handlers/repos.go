package handlers

import (
	"context"
	"fmt"

	"github.com/danielgtaylor/huma/v2"
	"github.com/getnvoi/nvoi/internal/api"
	"gorm.io/gorm"
)

// ── Input / Output types ─────────────────────────────────────────────────────

type ListReposInput struct {
	WorkspaceScopedInput
}

type ListReposOutput struct {
	Body []api.Repo
}

type CreateRepoInput struct {
	WorkspaceScopedInput
	Body struct {
		Name string `json:"name" required:"true" minLength:"1" doc:"Repo name"`
	}
}

type CreateRepoOutput struct {
	Body api.Repo
}

type GetRepoInput struct {
	RepoScopedInput
}

type GetRepoOutput struct {
	Body api.Repo
}

type UpdateRepoInput struct {
	RepoScopedInput
	Body struct {
		Name            string `json:"name,omitempty" doc:"New repo name"`
		ComputeProvider string `json:"compute_provider,omitempty" doc:"Compute provider alias"`
		DNSProvider     string `json:"dns_provider,omitempty" doc:"DNS provider alias"`
		StorageProvider string `json:"storage_provider,omitempty" doc:"Storage provider alias"`
		BuildProvider   string `json:"build_provider,omitempty" doc:"Build provider alias"`
		SecretsProvider string `json:"secrets_provider,omitempty" doc:"Secrets provider alias"`
	}
}

type UpdateRepoOutput struct {
	Body api.Repo
}

type DeleteRepoInput struct {
	RepoScopedInput
}

type DeleteRepoOutput struct {
	Body struct {
		Deleted bool `json:"deleted"`
	}
}

// ── Handlers ─────────────────────────────────────────────────────────────────

func ListRepos(db *gorm.DB) func(context.Context, *ListReposInput) (*ListReposOutput, error) {
	return func(ctx context.Context, input *ListReposInput) (*ListReposOutput, error) {
		user := api.UserFromContext(ctx)
		ws, err := findWorkspace(db, user.ID, input.WorkspaceID)
		if err != nil {
			return nil, err
		}

		var repos []api.Repo
		db.Where("workspace_id = ?", ws.ID).Find(&repos)
		return &ListReposOutput{Body: repos}, nil
	}
}

func CreateRepo(db *gorm.DB) func(context.Context, *CreateRepoInput) (*CreateRepoOutput, error) {
	return func(ctx context.Context, input *CreateRepoInput) (*CreateRepoOutput, error) {
		user := api.UserFromContext(ctx)
		ws, err := findWorkspace(db, user.ID, input.WorkspaceID)
		if err != nil {
			return nil, err
		}

		repo := api.Repo{
			WorkspaceID: ws.ID,
			Name:        input.Body.Name,
		}
		if err := db.Create(&repo).Error; err != nil {
			return nil, huma.Error500InternalServerError("failed to create repo")
		}

		return &CreateRepoOutput{Body: repo}, nil
	}
}

func GetRepo(db *gorm.DB) func(context.Context, *GetRepoInput) (*GetRepoOutput, error) {
	return func(ctx context.Context, input *GetRepoInput) (*GetRepoOutput, error) {
		user := api.UserFromContext(ctx)
		repo, err := findRepo(db, user.ID, input.WorkspaceID, input.RepoID)
		if err != nil {
			return nil, err
		}
		return &GetRepoOutput{Body: *repo}, nil
	}
}

func UpdateRepo(db *gorm.DB) func(context.Context, *UpdateRepoInput) (*UpdateRepoOutput, error) {
	return func(ctx context.Context, input *UpdateRepoInput) (*UpdateRepoOutput, error) {
		user := api.UserFromContext(ctx)
		repo, err := findRepo(db, user.ID, input.WorkspaceID, input.RepoID)
		if err != nil {
			return nil, err
		}

		if input.Body.Name != "" {
			db.Model(repo).Update("name", input.Body.Name)
		}

		// Link providers by alias — look up InfraProvider by alias, verify kind matches.
		providerLinks := []struct {
			alias string
			kind  api.ProviderKind
			field string
		}{
			{input.Body.ComputeProvider, api.ProviderKindCompute, "compute_provider_id"},
			{input.Body.DNSProvider, api.ProviderKindDNS, "dns_provider_id"},
			{input.Body.StorageProvider, api.ProviderKindStorage, "storage_provider_id"},
			{input.Body.BuildProvider, api.ProviderKindBuild, "build_provider_id"},
			{input.Body.SecretsProvider, api.ProviderKindSecrets, "secrets_provider_id"},
		}
		for _, link := range providerLinks {
			if link.alias == "" {
				continue
			}
			var prov api.InfraProvider
			if err := db.Where("workspace_id = ? AND alias = ?", repo.WorkspaceID, link.alias).First(&prov).Error; err != nil {
				return nil, huma.Error404NotFound(fmt.Sprintf("provider %q not found in workspace — run 'nvoi provider add' first", link.alias))
			}
			if prov.Kind != link.kind {
				return nil, huma.Error400BadRequest(fmt.Sprintf("provider %q is %s, not %s", link.alias, prov.Kind, link.kind))
			}
			db.Model(repo).Update(link.field, prov.ID)
		}

		// Reload with preloaded providers.
		repo, err = findRepo(db, user.ID, input.WorkspaceID, input.RepoID)
		if err != nil {
			return nil, err
		}
		return &UpdateRepoOutput{Body: *repo}, nil
	}
}

func DeleteRepo(db *gorm.DB) func(context.Context, *DeleteRepoInput) (*DeleteRepoOutput, error) {
	return func(ctx context.Context, input *DeleteRepoInput) (*DeleteRepoOutput, error) {
		user := api.UserFromContext(ctx)
		repo, err := findRepo(db, user.ID, input.WorkspaceID, input.RepoID)
		if err != nil {
			return nil, err
		}
		db.Delete(repo)
		return &DeleteRepoOutput{Body: struct {
			Deleted bool `json:"deleted"`
		}{Deleted: true}}, nil
	}
}

// ── Helpers ──────────────────────────────────────────────────────────────────

// findRepo fetches a repo scoped through workspace → user.
func findRepo(db *gorm.DB, userID, workspaceID, repoID string) (*api.Repo, error) {
	ws, err := findWorkspace(db, userID, workspaceID)
	if err != nil {
		return nil, err
	}

	var repo api.Repo
	q := db.Where("id = ? AND workspace_id = ?", repoID, ws.ID).
		Preload("ComputeProvider").
		Preload("DNSProvider").
		Preload("StorageProvider").
		Preload("BuildProvider").
		Preload("SecretsProvider")
	if err := q.First(&repo).Error; err != nil {
		return nil, huma.Error404NotFound("repo not found")
	}
	return &repo, nil
}
