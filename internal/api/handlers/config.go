package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/getnvoi/nvoi/internal/api"
	"github.com/getnvoi/nvoi/internal/api/config"
	"github.com/getnvoi/nvoi/internal/api/managed"
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

		// Parse config.
		cfg, err := config.Parse([]byte(req.Config))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid yaml: " + err.Error()})
			return
		}

		// Load stored managed service credentials for this repo.
		storedCreds := loadManagedCreds(db, repo.ID)

		// Expand managed services (replace with real specs, inject creds, add volumes).
		expanded, newCreds, err := managed.Expand(cfg, storedCreds)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		// Validate the expanded config (after managed services are resolved).
		errs := config.Validate(expanded)
		if len(errs) > 0 {
			msgs := make([]string, len(errs))
			for i, e := range errs {
				msgs[i] = e.Error()
			}
			c.JSON(http.StatusBadRequest, gin.H{"errors": msgs})
			return
		}

		// Merge managed service credential secrets into env for plan validation.
		env := config.ParseEnv(req.Env)
		for k, v := range managed.CredentialSecrets(mergeCreds(storedCreds, newCreds), cfg) {
			env[k] = v
		}

		// Validate that the plan can be built (env references resolve).
		if _, err := config.Plan(nil, expanded, env); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		// Persist new managed service credentials.
		for name, creds := range newCreds {
			svc := cfg.Services[name]
			credsJSON, _ := json.Marshal(creds)
			if err := db.Create(&api.RepoManagedServiceConfig{
				RepoID:      repo.ID,
				Name:        name,
				Kind:        svc.Managed,
				Credentials: string(credsJSON),
			}).Error; err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save managed credentials"})
				return
			}
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

		// Load previous config for diff.
		var prev *config.Config
		if rc.Version > 1 {
			var prevRC api.RepoConfig
			if err := db.Where("repo_id = ? AND version = ?", repo.ID, rc.Version-1).First(&prevRC).Error; err == nil {
				if prevCfg, err := config.Parse([]byte(prevRC.Config)); err == nil {
					prev, _, _ = managed.Expand(prevCfg, storedCreds)
				}
			}
		}

		steps, err := config.Plan(prev, expanded, env)
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

// ── helpers ────────────────────────────────────────────────────────────────────

// loadManagedCreds loads all managed service credentials for a repo
// into the map format Expand() expects: name → {key: value}.
func loadManagedCreds(db *gorm.DB, repoID string) map[string]map[string]string {
	var rows []api.RepoManagedServiceConfig
	db.Where("repo_id = ?", repoID).Find(&rows)

	creds := make(map[string]map[string]string, len(rows))
	for _, row := range rows {
		var m map[string]string
		if err := json.Unmarshal([]byte(row.Credentials), &m); err != nil {
			continue
		}
		creds[row.Name] = m
	}
	return creds
}

// mergeCreds merges stored and new credential maps. New takes precedence.
func mergeCreds(stored, newCreds map[string]map[string]string) map[string]map[string]string {
	merged := make(map[string]map[string]string, len(stored)+len(newCreds))
	for k, v := range stored {
		merged[k] = v
	}
	for k, v := range newCreds {
		merged[k] = v
	}
	return merged
}
