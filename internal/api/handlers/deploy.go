package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/getnvoi/nvoi/internal/api"
	"github.com/getnvoi/nvoi/internal/api/config"
	"github.com/getnvoi/nvoi/internal/api/managed"
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

		// Load previous config (for diff).
		var prev *api.RepoConfig
		var prevRC api.RepoConfig
		if rc.Version > 1 {
			if err := db.Where("repo_id = ? AND version = ?", repo.ID, rc.Version-1).First(&prevRC).Error; err == nil {
				prev = &prevRC
			}
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

		// Parse + expand previous config (if any).
		var prevExpanded *config.Config
		if prev != nil {
			prevCfg, err := config.Parse([]byte(prev.Config))
			if err == nil {
				prevExpanded, _, _ = managed.Expand(prevCfg, storedCreds)
			}
		}

		// Build full plan.
		steps, err := config.FullPlan(prevExpanded, expanded, env)
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

// markStepRunning updates a deployment step's status to running.
func markStepRunning(db *gorm.DB, step *api.DeploymentStep) {
	now := time.Now()
	db.Model(step).Updates(map[string]any{
		"status":     api.StepStatusRunning,
		"started_at": &now,
	})
}

// markStepDone updates a deployment step's status to succeeded or failed.
func markStepDone(db *gorm.DB, step *api.DeploymentStep, err error) {
	now := time.Now()
	if err != nil {
		db.Model(step).Updates(map[string]any{
			"status":      api.StepStatusFailed,
			"error":       err.Error(),
			"finished_at": &now,
		})
	} else {
		db.Model(step).Updates(map[string]any{
			"status":      api.StepStatusSucceeded,
			"finished_at": &now,
		})
	}
}

// markDeploymentRunning updates the deployment status to running.
func markDeploymentRunning(db *gorm.DB, deployment *api.Deployment) {
	now := time.Now()
	db.Model(deployment).Updates(map[string]any{
		"status":     api.DeploymentRunning,
		"started_at": &now,
	})
}

// markDeploymentDone updates the deployment status to succeeded or failed.
func markDeploymentDone(db *gorm.DB, deployment *api.Deployment, err error) {
	now := time.Now()
	status := api.DeploymentSucceeded
	if err != nil {
		status = api.DeploymentFailed
	}
	db.Model(deployment).Updates(map[string]any{
		"status":      status,
		"finished_at": &now,
	})
}

// skipRemainingSteps marks all pending steps as skipped.
func skipRemainingSteps(db *gorm.DB, deploymentID string) {
	db.Model(&api.DeploymentStep{}).
		Where("deployment_id = ? AND status = ?", deploymentID, api.StepStatusPending).
		Update("status", api.StepStatusSkipped)
}
