package handlers

import (
	"context"

	"github.com/danielgtaylor/huma/v2"
	"github.com/getnvoi/nvoi/internal/api"
	"gorm.io/gorm"
)

// ── Input / Output types ─────────────────────────────────────────────────────

type LoginInput struct {
	Body struct {
		GithubToken string `json:"github_token" required:"true" doc:"GitHub PAT, OAuth, or fine-grained token"`
	}
}

type LoginOutput struct {
	Body loginResponseBody
}

type loginResponseBody struct {
	Token     string        `json:"token" doc:"JWT bearer token (30-day TTL)"`
	User      api.User      `json:"user"`
	Workspace api.Workspace `json:"workspace"`
	IsNew     bool          `json:"is_new" doc:"True on first login"`
}

// ── Handler ──────────────────────────────────────────────────────────────────

func LoginHandler(db *gorm.DB, verify api.GitHubVerifier) func(context.Context, *LoginInput) (*LoginOutput, error) {
	return func(ctx context.Context, input *LoginInput) (*LoginOutput, error) {
		ghUser, err := verify(input.Body.GithubToken)
		if err != nil {
			return nil, huma.Error401Unauthorized("invalid github token: " + err.Error())
		}

		var user api.User
		var workspace api.Workspace
		isNew := false

		err = db.Transaction(func(tx *gorm.DB) error {
			result := tx.Where("github_username = ?", ghUser.Login).First(&user)
			if result.Error == gorm.ErrRecordNotFound {
				user = api.User{GithubUsername: ghUser.Login, GithubToken: input.Body.GithubToken}
				isNew = true
				if err := tx.Create(&user).Error; err != nil {
					return err
				}
			} else if result.Error != nil {
				return result.Error
			} else {
				user.GithubToken = input.Body.GithubToken
				if err := tx.Save(&user).Error; err != nil {
					return err
				}
			}

			err := tx.Joins("JOIN workspace_users ON workspace_users.workspace_id = workspaces.id").
				Where("workspace_users.user_id = ?", user.ID).
				First(&workspace).Error
			if err == gorm.ErrRecordNotFound {
				workspace = api.Workspace{
					Name:      "default",
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
			}
			return err
		})
		if err != nil {
			return nil, huma.Error500InternalServerError("login failed")
		}

		token, err := api.IssueToken(user.ID, user.GithubUsername)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to issue token")
		}

		return &LoginOutput{Body: loginResponseBody{
			Token:     token,
			User:      user,
			Workspace: workspace,
			IsNew:     isNew,
		}}, nil
	}
}
