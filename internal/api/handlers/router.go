package handlers

import (
	"github.com/getnvoi/nvoi/internal/api"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// NewRouter creates the API router.
// verify is the GitHub PAT verifier — pass api.VerifyGitHubPAT in production,
// or a fake in tests.
func NewRouter(db *gorm.DB, verify api.GitHubVerifier) *gin.Engine {
	r := gin.Default()

	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// Public
	r.POST("/login", LoginHandler(db, verify))

	// Authenticated
	auth := r.Group("/", api.AuthRequired(db))
	{
		ws := auth.Group("/workspaces")
		{
			ws.GET("", ListWorkspaces(db))
			ws.POST("", CreateWorkspace(db))
			ws.GET("/:workspace_id", GetWorkspace(db))
			ws.PUT("/:workspace_id", UpdateWorkspace(db))
			ws.DELETE("/:workspace_id", DeleteWorkspace(db))

			repos := ws.Group("/:workspace_id/repos")
			{
				repos.GET("", ListRepos(db))
				repos.POST("", CreateRepo(db))
				repos.GET("/:repo_id", GetRepo(db))
				repos.PUT("/:repo_id", UpdateRepo(db))
				repos.DELETE("/:repo_id", DeleteRepo(db))
			}
		}
	}

	return r
}
