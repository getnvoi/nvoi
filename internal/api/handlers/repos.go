package handlers

import (
	"net/http"

	"github.com/getnvoi/nvoi/internal/api"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ListRepos returns all repos in a workspace.
//
// @Summary     List repos
// @Description Returns all repos in the specified workspace.
// @Tags        repos
// @Produce     json
// @Security    BearerAuth
// @Param       workspace_id path     string true "Workspace ID" format(uuid)
// @Success     200          {array}  api.Repo
// @Failure     401          {object} errorResponse
// @Failure     404          {object} errorResponse
// @Router      /workspaces/{workspace_id}/repos [get]
func ListRepos(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		ws, ok := loadWorkspace(c, db)
		if !ok {
			return
		}

		var repos []api.Repo
		db.Where("workspace_id = ?", ws.ID).Find(&repos)
		c.JSON(http.StatusOK, repos)
	}
}

// CreateRepo creates a new repo with an auto-generated SSH keypair.
//
// @Summary     Create repo
// @Description Creates a new repo in the workspace. An Ed25519 SSH keypair is auto-generated for server access.
// @Tags        repos
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       workspace_id path     string            true "Workspace ID" format(uuid)
// @Param       body         body     createRepoRequest true "Repo name"
// @Success     201          {object} api.Repo
// @Failure     400          {object} errorResponse
// @Failure     401          {object} errorResponse
// @Failure     404          {object} errorResponse
// @Failure     500          {object} errorResponse
// @Router      /workspaces/{workspace_id}/repos [post]
func CreateRepo(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		ws, ok := loadWorkspace(c, db)
		if !ok {
			return
		}

		var req createRepoRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		repo := api.Repo{
			WorkspaceID: ws.ID,
			Name:        req.Name,
		}
		if err := db.Create(&repo).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create repo"})
			return
		}

		c.JSON(http.StatusCreated, repo)
	}
}

// GetRepo returns a single repo by ID.
//
// @Summary     Get repo
// @Description Returns a repo by ID, scoped through the workspace.
// @Tags        repos
// @Produce     json
// @Security    BearerAuth
// @Param       workspace_id path     string true "Workspace ID" format(uuid)
// @Param       repo_id      path     string true "Repo ID"      format(uuid)
// @Success     200          {object} api.Repo
// @Failure     401          {object} errorResponse
// @Failure     404          {object} errorResponse
// @Router      /workspaces/{workspace_id}/repos/{repo_id} [get]
func GetRepo(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		repo, ok := loadRepo(c, db)
		if !ok {
			return
		}
		c.JSON(http.StatusOK, repo)
	}
}

// UpdateRepo renames a repo.
//
// @Summary     Update repo
// @Description Renames a repo by ID.
// @Tags        repos
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       workspace_id path     string            true "Workspace ID" format(uuid)
// @Param       repo_id      path     string            true "Repo ID"      format(uuid)
// @Param       body         body     updateRepoRequest true "New name"
// @Success     200          {object} api.Repo
// @Failure     400          {object} errorResponse
// @Failure     401          {object} errorResponse
// @Failure     404          {object} errorResponse
// @Router      /workspaces/{workspace_id}/repos/{repo_id} [put]
func UpdateRepo(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		repo, ok := loadRepo(c, db)
		if !ok {
			return
		}

		var req updateRepoRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		db.Model(repo).Update("name", req.Name)
		c.JSON(http.StatusOK, repo)
	}
}

// DeleteRepo soft-deletes a repo.
//
// @Summary     Delete repo
// @Description Soft-deletes a repo by ID.
// @Tags        repos
// @Produce     json
// @Security    BearerAuth
// @Param       workspace_id path     string true "Workspace ID" format(uuid)
// @Param       repo_id      path     string true "Repo ID"      format(uuid)
// @Success     200          {object} deleteResponse
// @Failure     401          {object} errorResponse
// @Failure     404          {object} errorResponse
// @Router      /workspaces/{workspace_id}/repos/{repo_id} [delete]
func DeleteRepo(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		repo, ok := loadRepo(c, db)
		if !ok {
			return
		}
		db.Delete(repo)
		c.JSON(http.StatusOK, gin.H{"deleted": true})
	}
}

// loadRepo fetches a repo scoped through workspace → user.
func loadRepo(c *gin.Context, db *gorm.DB) (*api.Repo, bool) {
	ws, ok := loadWorkspace(c, db)
	if !ok {
		return nil, false
	}

	repoID := c.Param("repo_id")
	var repo api.Repo
	if err := db.Where("id = ? AND workspace_id = ?", repoID, ws.ID).First(&repo).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "repo not found"})
		return nil, false
	}
	return &repo, true
}
