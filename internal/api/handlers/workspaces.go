package handlers

import (
	"net/http"

	"github.com/getnvoi/nvoi/internal/api"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ListWorkspaces returns all workspaces the authenticated user belongs to.
//
// @Summary     List workspaces
// @Description Returns all workspaces the authenticated user is a member of.
// @Tags        workspaces
// @Produce     json
// @Security    BearerAuth
// @Success     200 {array}  api.Workspace
// @Failure     401 {object} errorResponse
// @Router      /workspaces [get]
func ListWorkspaces(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		user := api.CurrentUser(c)

		var workspaces []api.Workspace
		db.Joins("JOIN workspace_users ON workspace_users.workspace_id = workspaces.id").
			Where("workspace_users.user_id = ?", user.ID).
			Find(&workspaces)

		c.JSON(http.StatusOK, workspaces)
	}
}

// CreateWorkspace creates a new workspace owned by the authenticated user.
//
// @Summary     Create workspace
// @Description Creates a new workspace and assigns the authenticated user as owner.
// @Tags        workspaces
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       body body     createWorkspaceRequest true "Workspace name"
// @Success     201  {object} api.Workspace
// @Failure     400  {object} errorResponse
// @Failure     401  {object} errorResponse
// @Failure     500  {object} errorResponse
// @Router      /workspaces [post]
func CreateWorkspace(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		user := api.CurrentUser(c)

		var req createWorkspaceRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		var workspace api.Workspace
		err := db.Transaction(func(tx *gorm.DB) error {
			workspace = api.Workspace{
				Name:      req.Name,
				CreatedBy: user.ID,
			}
			if err := tx.Create(&workspace).Error; err != nil {
				return err
			}
			return tx.Create(&api.WorkspaceUser{
				UserID:      user.ID,
				WorkspaceID: workspace.ID,
				Role:        "owner",
			}).Error
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create workspace"})
			return
		}

		c.JSON(http.StatusCreated, workspace)
	}
}

// GetWorkspace returns a single workspace by ID.
//
// @Summary     Get workspace
// @Description Returns a workspace by ID, scoped to the authenticated user.
// @Tags        workspaces
// @Produce     json
// @Security    BearerAuth
// @Param       workspace_id path     string true "Workspace ID" format(uuid)
// @Success     200          {object} api.Workspace
// @Failure     401          {object} errorResponse
// @Failure     404          {object} errorResponse
// @Router      /workspaces/{workspace_id} [get]
func GetWorkspace(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		ws, ok := loadWorkspace(c, db)
		if !ok {
			return
		}
		c.JSON(http.StatusOK, ws)
	}
}

// UpdateWorkspace renames a workspace.
//
// @Summary     Update workspace
// @Description Renames a workspace by ID.
// @Tags        workspaces
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       workspace_id path     string                 true "Workspace ID" format(uuid)
// @Param       body         body     updateWorkspaceRequest true "New name"
// @Success     200          {object} api.Workspace
// @Failure     400          {object} errorResponse
// @Failure     401          {object} errorResponse
// @Failure     404          {object} errorResponse
// @Router      /workspaces/{workspace_id} [put]
func UpdateWorkspace(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		ws, ok := loadWorkspace(c, db)
		if !ok {
			return
		}

		var req updateWorkspaceRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		db.Model(ws).Update("name", req.Name)
		c.JSON(http.StatusOK, ws)
	}
}

// DeleteWorkspace soft-deletes a workspace.
//
// @Summary     Delete workspace
// @Description Soft-deletes a workspace by ID.
// @Tags        workspaces
// @Produce     json
// @Security    BearerAuth
// @Param       workspace_id path     string true "Workspace ID" format(uuid)
// @Success     200          {object} deleteResponse
// @Failure     401          {object} errorResponse
// @Failure     404          {object} errorResponse
// @Router      /workspaces/{workspace_id} [delete]
func DeleteWorkspace(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		ws, ok := loadWorkspace(c, db)
		if !ok {
			return
		}
		db.Delete(ws)
		c.JSON(http.StatusOK, gin.H{"deleted": true, "name": ws.Name})
	}
}

// loadWorkspace fetches the workspace scoped to the current user.
func loadWorkspace(c *gin.Context, db *gorm.DB) (*api.Workspace, bool) {
	user := api.CurrentUser(c)
	id := c.Param("workspace_id")

	var ws api.Workspace
	result := db.
		Joins("JOIN workspace_users ON workspace_users.workspace_id = workspaces.id").
		Where("workspaces.id = ? AND workspace_users.user_id = ?", id, user.ID).
		First(&ws)

	if result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "workspace not found"})
		return nil, false
	}
	return &ws, true
}
