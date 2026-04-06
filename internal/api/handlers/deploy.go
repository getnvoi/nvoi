package handlers

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/getnvoi/nvoi/internal/api"
	"github.com/getnvoi/nvoi/internal/api/config"
	"github.com/getnvoi/nvoi/internal/api/managed"
	"github.com/getnvoi/nvoi/internal/api/plan"
	pkgcore "github.com/getnvoi/nvoi/pkg/core"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Deploy creates a deployment from the latest config, computes the full plan
// (diff + set), persists steps, and returns the deployment.
// Execution is synchronous for now — the caller can poll step statuses.
//
// POST /workspaces/:workspace_id/repos/:repo_id/deploy
func Deploy(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		repo, ok := loadRepo(c, db)
		if !ok {
			return
		}

		// Load latest config.
		var rc api.RepoConfig
		if err := db.Where("repo_id = ?", repo.ID).Order("version DESC").First(&rc).Error; err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no config found — push a config first"})
			return
		}

		// Parse + expand current config.
		cfg, err := config.Parse([]byte(rc.Config))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "corrupt config: " + err.Error()})
			return
		}

		storedCreds := loadManagedCreds(db, repo.ID)
		expanded, _, err := managed.Expand(cfg, storedCreds)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "expand failed: " + err.Error()})
			return
		}

		env := config.ParseEnv(rc.Env)
		for k, v := range managed.CredentialSecrets(storedCreds, cfg) {
			env[k] = v
		}

		// Query reality — what's actually deployed.
		creds, credErr := resolveAllCredentials(&rc, env)
		var reality *config.Config
		if credErr == nil {
			reality = plan.InfraState(c.Request.Context(), plan.InfraStateRequest{
				Cluster: pkgcore.Cluster{
					AppName:     repo.Name,
					Env:         repo.Environment,
					Provider:    string(rc.ComputeProvider),
					Credentials: creds.Compute,
					SSHKey:      []byte(repo.SSHPrivateKey),
				},
				DNS:     pkgcore.ProviderRef{Name: string(rc.DNSProvider), Creds: creds.DNS},
				Storage: pkgcore.ProviderRef{Name: string(rc.StorageProvider), Creds: creds.Storage},
			})
		}

		// Build plan: reality vs desired.
		steps, err := plan.Build(reality, expanded, env)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "plan failed: " + err.Error()})
			return
		}

		// Create deployment + steps in a transaction.
		var deployment api.Deployment
		err = db.Transaction(func(tx *gorm.DB) error {
			deployment = api.Deployment{
				RepoID:       repo.ID,
				RepoConfigID: rc.ID,
				Status:       api.DeploymentPending,
			}
			if err := tx.Create(&deployment).Error; err != nil {
				return err
			}

			for i, step := range steps {
				paramsJSON, _ := json.Marshal(step.Params)
				if err := tx.Create(&api.DeploymentStep{
					DeploymentID: deployment.ID,
					Position:     i + 1,
					Kind:         string(step.Kind),
					Name:         step.Name,
					Params:       string(paramsJSON),
					Status:       api.StepStatusPending,
				}).Error; err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create deployment"})
			return
		}

		// Load back with steps.
		db.Preload("Steps", func(db *gorm.DB) *gorm.DB {
			return db.Order("position")
		}).First(&deployment, "id = ?", deployment.ID)

		c.JSON(http.StatusCreated, deployment)
	}
}

// RunDeployment starts executing a pending deployment.
// Separate from Deploy so creation and execution are decoupled.
//
// POST /workspaces/:workspace_id/repos/:repo_id/deployments/:deployment_id/run
func RunDeployment(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		repo, ok := loadRepo(c, db)
		if !ok {
			return
		}

		deploymentID := c.Param("deployment_id")
		var deployment api.Deployment
		if err := db.Where("id = ? AND repo_id = ?", deploymentID, repo.ID).First(&deployment).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "deployment not found"})
			return
		}

		if deployment.Status != api.DeploymentPending {
			c.JSON(http.StatusBadRequest, gin.H{"error": "deployment is not pending"})
			return
		}

		var rc api.RepoConfig
		if err := db.First(&rc, "id = ?", deployment.RepoConfigID).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "config not found"})
			return
		}

		// Load user for git token.
		user := api.CurrentUser(c)

		env := config.ParseEnv(rc.Env)

		go Execute(context.Background(), db, ExecuteParams{
			Deployment: &deployment,
			Repo:       repo,
			Config:     &rc,
			Env:        env,
			GitToken:   user.GithubToken,
		})

		c.JSON(http.StatusOK, gin.H{"status": "running"})
	}
}

// GetDeployment returns a deployment with its steps and logs.
//
// GET /workspaces/:workspace_id/repos/:repo_id/deployments/:deployment_id
func GetDeployment(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		repo, ok := loadRepo(c, db)
		if !ok {
			return
		}

		deploymentID := c.Param("deployment_id")
		var deployment api.Deployment
		result := db.
			Preload("Steps", func(db *gorm.DB) *gorm.DB {
				return db.Order("position")
			}).
			Preload("Steps.Logs").
			Where("id = ? AND repo_id = ?", deploymentID, repo.ID).
			First(&deployment)

		if result.Error != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "deployment not found"})
			return
		}

		c.JSON(http.StatusOK, deployment)
	}
}

// ListDeployments returns all deployments for a repo.
//
// GET /workspaces/:workspace_id/repos/:repo_id/deployments
func ListDeployments(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		repo, ok := loadRepo(c, db)
		if !ok {
			return
		}

		var deployments []api.Deployment
		db.Where("repo_id = ?", repo.ID).Order("created_at DESC").Find(&deployments)
		c.JSON(http.StatusOK, deployments)
	}
}

// DeploymentLogs returns all log lines for a deployment as JSONL.
// Each line is a raw event from pkg/core.Event — the CLI renders it.
//
// GET /workspaces/:workspace_id/repos/:repo_id/deployments/:deployment_id/logs
func DeploymentLogs(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		repo, ok := loadRepo(c, db)
		if !ok {
			return
		}

		deploymentID := c.Param("deployment_id")
		var deployment api.Deployment
		if err := db.Where("id = ? AND repo_id = ?", deploymentID, repo.ID).First(&deployment).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "deployment not found"})
			return
		}

		// Load steps in order with their logs.
		var steps []api.DeploymentStep
		db.Where("deployment_id = ?", deployment.ID).
			Order("position").
			Preload("Logs").
			Find(&steps)

		// Stream as JSONL — one line per log entry, in step order.
		c.Header("Content-Type", "application/x-ndjson")
		c.Writer.WriteHeader(http.StatusOK)
		for _, step := range steps {
			for _, log := range step.Logs {
				c.Writer.Write([]byte(log.Line))
				c.Writer.Write([]byte("\n"))
			}
		}
	}
}

