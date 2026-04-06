package handlers

import (
	"net/http"

	"github.com/getnvoi/nvoi/internal/api"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type loginRequest struct {
	GithubToken string `json:"github_token" binding:"required"`
}

type loginResponse struct {
	Token     string        `json:"token"`
	User      api.User      `json:"user"`
	Workspace api.Workspace `json:"workspace"`
	IsNew     bool          `json:"is_new"`
}

// LoginHandler exchanges a GitHub token for a JWT.
//
// @Summary     Login with GitHub token
// @Description Verifies a GitHub personal access token and returns a JWT. Creates the user and a default workspace on first login.
// @Tags        auth
// @Accept      json
// @Produce     json
// @Param       body body     loginRequest  true "GitHub token"
// @Success     200  {object} loginResponse
// @Failure     400  {object} errorResponse
// @Failure     401  {object} errorResponse
// @Failure     500  {object} errorResponse
// @Router      /login [post]
func LoginHandler(db *gorm.DB, verify api.GitHubVerifier) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req loginRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		ghUser, err := verify(req.GithubToken)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid github token: " + err.Error()})
			return
		}

		var user api.User
		var workspace api.Workspace
		isNew := false

		err = db.Transaction(func(tx *gorm.DB) error {
			// Find or create user.
			result := tx.Where("github_username = ?", ghUser.Login).First(&user)
			if result.Error == gorm.ErrRecordNotFound {
				user = api.User{GithubUsername: ghUser.Login, GithubToken: req.GithubToken}
				isNew = true
				if err := tx.Create(&user).Error; err != nil {
					return err
				}
			} else if result.Error != nil {
				return result.Error
			} else {
				// Existing user — update token (may have been refreshed).
				user.GithubToken = req.GithubToken
				if err := tx.Save(&user).Error; err != nil {
					return err
				}
			}

			// Find or create default workspace.
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
			c.JSON(http.StatusInternalServerError, gin.H{"error": "login failed"})
			return
		}

		token, err := api.IssueToken(user.ID, user.GithubUsername)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to issue token"})
			return
		}

		c.JSON(http.StatusOK, loginResponse{
			Token:     token,
			User:      user,
			Workspace: workspace,
			IsNew:     isNew,
		})
	}
}
