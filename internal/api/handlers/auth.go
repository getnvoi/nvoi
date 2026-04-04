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
	Token string   `json:"token"`
	User  api.User `json:"user"`
	IsNew bool     `json:"is_new"`
}

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
		isNew := false

		err = db.Transaction(func(tx *gorm.DB) error {
			result := tx.Where("github_username = ?", ghUser.Login).First(&user)
			if result.Error == gorm.ErrRecordNotFound {
				user = api.User{GithubUsername: ghUser.Login}
				isNew = true
				return tx.Create(&user).Error
			}
			return result.Error
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
			Token: token,
			User:  user,
			IsNew: isNew,
		})
	}
}
