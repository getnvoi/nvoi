package handlers

import (
	"github.com/getnvoi/nvoi/internal/api"
	_ "github.com/getnvoi/nvoi/internal/api/docs" // swagger generated docs
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"gorm.io/gorm"
)

// @title       nvoi API
// @version     1.0
// @description Deploy containers to cloud servers. Push a config YAML + env, get an ordered deployment plan, execute it.

// @host     localhost:8080
// @BasePath /

// @securityDefinitions.apikey BearerAuth
// @in                         header
// @name                       Authorization
// @description                JWT token from POST /login. Format: "Bearer {token}"

// NewRouter creates the API router.
// verify is the GitHub PAT verifier — pass api.VerifyGitHubToken in production,
// or a fake in tests.
func NewRouter(db *gorm.DB, verify api.GitHubVerifier) *gin.Engine {
	r := gin.Default()

	// Swagger UI
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	// @Summary     Health check
	// @Description Returns API health status.
	// @Tags        system
	// @Produce     json
	// @Success     200 {object} healthResponse
	// @Router      /health [get]
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

				// Config (versioned)
				repos.POST("/:repo_id/config", PushConfig(db))
				repos.GET("/:repo_id/config", GetConfig(db))
				repos.GET("/:repo_id/configs", ListConfigs(db))
				repos.GET("/:repo_id/config/plan", PlanConfig(db))

				// Live cluster
				repos.GET("/:repo_id/describe", DescribeCluster(db))
				repos.GET("/:repo_id/resources", ListResources(db))
				repos.POST("/:repo_id/ssh", RunSSH(db))

				// Deploy
				repos.POST("/:repo_id/deploy", Deploy(db))
				repos.GET("/:repo_id/deployments", ListDeployments(db))
				repos.GET("/:repo_id/deployments/:deployment_id", GetDeployment(db))
				repos.POST("/:repo_id/deployments/:deployment_id/run", RunDeployment(db))
				repos.GET("/:repo_id/deployments/:deployment_id/logs", DeploymentLogs(db))
			}
		}
	}

	return r
}
