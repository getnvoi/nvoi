package handlers

import (
	"net/http"

	"github.com/getnvoi/nvoi/internal/api"
	"github.com/getnvoi/nvoi/internal/api/config"
	pkgcore "github.com/getnvoi/nvoi/pkg/core"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// DescribeCluster returns the live cluster state for a repo's latest config.
//
// @Summary     Describe cluster
// @Description Returns live cluster state (nodes, workloads, pods, services, ingress, secrets, storage) via SSH. Same data as `nvoi describe` in direct mode.
// @Tags        cluster
// @Produce     json
// @Security    BearerAuth
// @Param       workspace_id path     string true "Workspace ID" format(uuid)
// @Param       repo_id      path     string true "Repo ID"      format(uuid)
// @Success     200          {object} github_com_getnvoi_nvoi_pkg_core.DescribeResult
// @Failure     400          {object} errorResponse
// @Failure     401          {object} errorResponse
// @Failure     404          {object} errorResponse
// @Failure     500          {object} errorResponse
// @Router      /workspaces/{workspace_id}/repos/{repo_id}/describe [get]
func DescribeCluster(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		repo, ok := loadRepo(c, db)
		if !ok {
			return
		}

		cluster, err := clusterFromLatestConfig(db, repo)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		res, err := pkgcore.Describe(c.Request.Context(), pkgcore.DescribeRequest{Cluster: *cluster})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, res)
	}
}

// ListResources returns all provider resources for a repo's config.
//
// @Summary     List provider resources
// @Description Returns all resources across configured providers (compute, DNS, storage). Same data as `nvoi resources` in direct mode.
// @Tags        cluster
// @Produce     json
// @Security    BearerAuth
// @Param       workspace_id path     string true "Workspace ID" format(uuid)
// @Param       repo_id      path     string true "Repo ID"      format(uuid)
// @Success     200          {array}  github_com_getnvoi_nvoi_pkg_provider.ResourceGroup
// @Failure     400          {object} errorResponse
// @Failure     401          {object} errorResponse
// @Failure     404          {object} errorResponse
// @Failure     500          {object} errorResponse
// @Router      /workspaces/{workspace_id}/repos/{repo_id}/resources [get]
func ListResources(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		repo, ok := loadRepo(c, db)
		if !ok {
			return
		}

		rc, env, err := latestConfigAndEnv(db, repo)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		creds, err := resolveAllCredentials(rc, env)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		req := pkgcore.ResourcesRequest{
			Compute: pkgcore.ProviderRef{Name: string(rc.ComputeProvider), Creds: creds.Compute},
		}
		if rc.DNSProvider != "" {
			req.DNS = pkgcore.ProviderRef{Name: string(rc.DNSProvider), Creds: creds.DNS}
		}
		if rc.StorageProvider != "" {
			req.Storage = pkgcore.ProviderRef{Name: string(rc.StorageProvider), Creds: creds.Storage}
		}

		groups, err := pkgcore.Resources(c.Request.Context(), req)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, groups)
	}
}

// clusterFromLatestConfig builds a pkg/core.Cluster from the repo's latest config.
// All fields come from the DB — never from env string lookups.
func clusterFromLatestConfig(db *gorm.DB, repo *api.Repo) (*pkgcore.Cluster, error) {
	rc, env, err := latestConfigAndEnv(db, repo)
	if err != nil {
		return nil, err
	}

	creds, err := resolveAllCredentials(rc, env)
	if err != nil {
		return nil, err
	}

	return &pkgcore.Cluster{
		AppName:     repo.Name,
		Env:         repo.Environment,
		Provider:    string(rc.ComputeProvider),
		Credentials: creds.Compute,
		SSHKey:      []byte(repo.SSHPrivateKey),
	}, nil
}

// latestConfigAndEnv loads the latest RepoConfig and parses its env.
func latestConfigAndEnv(db *gorm.DB, repo *api.Repo) (*api.RepoConfig, map[string]string, error) {
	var rc api.RepoConfig
	if err := db.Where("repo_id = ?", repo.ID).Order("version DESC").First(&rc).Error; err != nil {
		return nil, nil, err
	}
	env := config.ParseEnv(rc.Env)
	return &rc, env, nil
}
