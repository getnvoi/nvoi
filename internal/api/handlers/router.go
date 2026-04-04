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
	_ = r.Group("/", api.AuthRequired(db))

	return r
}
