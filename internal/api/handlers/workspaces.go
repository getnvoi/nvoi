package handlers

import (
	"context"

	"github.com/danielgtaylor/huma/v2"
	"github.com/getnvoi/nvoi/internal/api"
	"gorm.io/gorm"
)

// ── Input / Output types ─────────────────────────────────────────────────────

type ListWorkspacesOutput struct {
	Body []api.Workspace
}

type CreateWorkspaceInput struct {
	Body struct {
		Name string `json:"name" required:"true" minLength:"1" doc:"Workspace name"`
	}
}

type CreateWorkspaceOutput struct {
	Body api.Workspace
}

type GetWorkspaceInput struct {
	WorkspaceScopedInput
}

type GetWorkspaceOutput struct {
	Body api.Workspace
}

type UpdateWorkspaceInput struct {
	WorkspaceScopedInput
	Body struct {
		Name string `json:"name" required:"true" minLength:"1" doc:"New workspace name"`
	}
}

type UpdateWorkspaceOutput struct {
	Body api.Workspace
}

type DeleteWorkspaceInput struct {
	WorkspaceScopedInput
}

type DeleteWorkspaceOutput struct {
	Body struct {
		Deleted bool   `json:"deleted"`
		Name    string `json:"name,omitempty"`
	}
}

// ── Handlers ─────────────────────────────────────────────────────────────────

func ListWorkspaces(db *gorm.DB) func(context.Context, *struct{}) (*ListWorkspacesOutput, error) {
	return func(ctx context.Context, _ *struct{}) (*ListWorkspacesOutput, error) {
		user := api.UserFromContext(ctx)

		var workspaces []api.Workspace
		db.Joins("JOIN workspace_users ON workspace_users.workspace_id = workspaces.id").
			Where("workspace_users.user_id = ?", user.ID).
			Find(&workspaces)

		return &ListWorkspacesOutput{Body: workspaces}, nil
	}
}

func CreateWorkspace(db *gorm.DB) func(context.Context, *CreateWorkspaceInput) (*CreateWorkspaceOutput, error) {
	return func(ctx context.Context, input *CreateWorkspaceInput) (*CreateWorkspaceOutput, error) {
		user := api.UserFromContext(ctx)

		var workspace api.Workspace
		err := db.Transaction(func(tx *gorm.DB) error {
			workspace = api.Workspace{
				Name:      input.Body.Name,
				CreatedBy: user.ID,
			}
			if err := tx.Create(&workspace).Error; err != nil {
				return err
			}
			return tx.Create(&api.WorkspaceUser{
				UserID:      user.ID,
				WorkspaceID: workspace.ID,
				Role:        "owner",
			}).Error
		})
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to create workspace")
		}

		return &CreateWorkspaceOutput{Body: workspace}, nil
	}
}

func GetWorkspace(db *gorm.DB) func(context.Context, *GetWorkspaceInput) (*GetWorkspaceOutput, error) {
	return func(ctx context.Context, input *GetWorkspaceInput) (*GetWorkspaceOutput, error) {
		user := api.UserFromContext(ctx)
		ws, err := findWorkspace(db, user.ID, input.WorkspaceID)
		if err != nil {
			return nil, err
		}
		return &GetWorkspaceOutput{Body: *ws}, nil
	}
}

func UpdateWorkspace(db *gorm.DB) func(context.Context, *UpdateWorkspaceInput) (*UpdateWorkspaceOutput, error) {
	return func(ctx context.Context, input *UpdateWorkspaceInput) (*UpdateWorkspaceOutput, error) {
		user := api.UserFromContext(ctx)
		ws, err := findWorkspace(db, user.ID, input.WorkspaceID)
		if err != nil {
			return nil, err
		}

		db.Model(ws).Update("name", input.Body.Name)
		return &UpdateWorkspaceOutput{Body: *ws}, nil
	}
}

func DeleteWorkspace(db *gorm.DB) func(context.Context, *DeleteWorkspaceInput) (*DeleteWorkspaceOutput, error) {
	return func(ctx context.Context, input *DeleteWorkspaceInput) (*DeleteWorkspaceOutput, error) {
		user := api.UserFromContext(ctx)
		ws, err := findWorkspace(db, user.ID, input.WorkspaceID)
		if err != nil {
			return nil, err
		}
		db.Delete(ws)
		return &DeleteWorkspaceOutput{Body: struct {
			Deleted bool   `json:"deleted"`
			Name    string `json:"name,omitempty"`
		}{Deleted: true, Name: ws.Name}}, nil
	}
}

// ── Helpers ──────────────────────────────────────────────────────────────────

// findWorkspace fetches a workspace scoped to the given user.
func findWorkspace(db *gorm.DB, userID, workspaceID string) (*api.Workspace, error) {
	var ws api.Workspace
	result := db.
		Joins("JOIN workspace_users ON workspace_users.workspace_id = workspaces.id").
		Where("workspaces.id = ? AND workspace_users.user_id = ?", workspaceID, userID).
		First(&ws)

	if result.Error != nil {
		return nil, huma.Error404NotFound("workspace not found")
	}
	return &ws, nil
}
