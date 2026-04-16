package handlers

import (
	"context"

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

// CreateRepoOutput includes the agent token in plaintext — returned once at creation.
// The token is not stored in the DB (only the hash is). This is the only time the user
// can see the token, for setting NVOI_API_TOKEN in the agent's .env.
type CreateRepoOutput struct {
	Body struct {
		api.Repo
		AgentToken string `json:"agent_token"` // plaintext — shown once, then only hash is used
	}
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
		Name string `json:"name,omitempty" doc:"New repo name"`
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

		out := &CreateRepoOutput{}
		out.Body.Repo = repo
		out.Body.AgentToken = repo.AgentToken // transient — in-memory only after BeforeCreate
		return out, nil
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
	if err := db.Where("id = ? AND workspace_id = ?", repoID, ws.ID).First(&repo).Error; err != nil {
		return nil, huma.Error404NotFound("repo not found")
	}
	return &repo, nil
}
