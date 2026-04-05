package handlers

import (
	"net/http"
	"strings"

	"github.com/getnvoi/nvoi/internal/api"
	"github.com/getnvoi/nvoi/internal/api/config"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// PushConfig stores a new versioned config snapshot for a repo.
// Validates the YAML config and optionally validates env references.
//
// POST /workspaces/:workspace_id/repos/:repo_id/config
func PushConfig(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		repo, ok := loadRepo(c, db)
		if !ok {
			return
		}

		var req struct {
			ComputeProvider api.ComputeProvider `json:"compute_provider" binding:"required"`
			DNSProvider     api.DNSProvider     `json:"dns_provider,omitempty"`
			StorageProvider api.StorageProvider  `json:"storage_provider,omitempty"`
			BuildProvider   api.BuildProvider    `json:"build_provider,omitempty"`
			Config          string               `json:"config" binding:"required"` // YAML
			Env             string               `json:"env,omitempty"`             // KEY=VALUE pairs
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		// Validate provider enums.
		rc := api.RepoConfig{
			ComputeProvider: req.ComputeProvider,
			DNSProvider:     req.DNSProvider,
			StorageProvider: req.StorageProvider,
			BuildProvider:   req.BuildProvider,
		}
		if err := rc.ValidateProviders(); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		// Parse and validate config.
		cfg, err := config.Parse([]byte(req.Config))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid yaml: " + err.Error()})
			return
		}

		errs := config.Validate(cfg)
		if len(errs) > 0 {
			msgs := make([]string, len(errs))
			for i, e := range errs {
				msgs[i] = e.Error()
			}
			c.JSON(http.StatusBadRequest, gin.H{"errors": msgs})
			return
		}

		// Validate that the plan can be built (env references resolve).
		env := config.ParseEnv(req.Env)
		if _, err := config.Plan(cfg, env); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		// Next version number.
		var maxVersion int
		db.Model(&api.RepoConfig{}).
			Where("repo_id = ?", repo.ID).
			Select("COALESCE(MAX(version), 0)").
			Scan(&maxVersion)

		rc.RepoID = repo.ID
		rc.Version = maxVersion + 1
		rc.Config = req.Config
		rc.Env = req.Env
		if err := db.Create(&rc).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save config"})
			return
		}

		c.JSON(http.StatusCreated, rc)
	}
}

// GetConfig returns the latest config for a repo.
//
// GET /workspaces/:workspace_id/repos/:repo_id/config
func GetConfig(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		repo, ok := loadRepo(c, db)
		if !ok {
			return
		}

		var rc api.RepoConfig
		result := db.Where("repo_id = ?", repo.ID).Order("version DESC").First(&rc)
		if result.Error != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "no config found"})
			return
		}

		// Reveal env only if ?reveal=true.
		reveal := strings.ToLower(c.DefaultQuery("reveal", "")) == "true"
		type response struct {
			api.RepoConfig
			Env string `json:"env,omitempty"`
		}
		resp := response{RepoConfig: rc}
		if reveal {
			resp.Env = rc.Env
		}

		c.JSON(http.StatusOK, resp)
	}
}

// ListConfigs returns all config versions for a repo.
//
// GET /workspaces/:workspace_id/repos/:repo_id/configs
func ListConfigs(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		repo, ok := loadRepo(c, db)
		if !ok {
			return
		}

		var configs []api.RepoConfig
		db.Where("repo_id = ?", repo.ID).Order("version DESC").Find(&configs)

		// Strip env from list response.
		type item struct {
			ID              string              `json:"id"`
			Version         int                 `json:"version"`
			ComputeProvider api.ComputeProvider `json:"compute_provider"`
			DNSProvider     api.DNSProvider     `json:"dns_provider,omitempty"`
			StorageProvider api.StorageProvider  `json:"storage_provider,omitempty"`
			BuildProvider   api.BuildProvider    `json:"build_provider,omitempty"`
			Config          string               `json:"config"`
		}
		out := make([]item, len(configs))
		for i, rc := range configs {
			out[i] = item{
				ID: rc.ID, Version: rc.Version,
				ComputeProvider: rc.ComputeProvider,
				DNSProvider:     rc.DNSProvider,
				StorageProvider: rc.StorageProvider,
				BuildProvider:   rc.BuildProvider,
				Config:          rc.Config,
			}
		}

		c.JSON(http.StatusOK, out)
	}
}

// PlanConfig returns the execution plan for the latest (or specified) config.
//
// GET /workspaces/:workspace_id/repos/:repo_id/config/plan
func PlanConfig(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		repo, ok := loadRepo(c, db)
		if !ok {
			return
		}

		var rc api.RepoConfig
		result := db.Where("repo_id = ?", repo.ID).Order("version DESC").First(&rc)
		if result.Error != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "no config found"})
			return
		}

		cfg, err := config.Parse([]byte(rc.Config))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "corrupt config: " + err.Error()})
			return
		}

		env := config.ParseEnv(rc.Env)
		steps, err := config.Plan(cfg, env)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "plan failed: " + err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"version": rc.Version,
			"steps":   steps,
		})
	}
}
