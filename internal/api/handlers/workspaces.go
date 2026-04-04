package handlers

import (
	"net/http"

	"github.com/getnvoi/nvoi/internal/api"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

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

func CreateWorkspace(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		user := api.CurrentUser(c)

		var req struct {
			Name string `json:"name" binding:"required"`
		}
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

func GetWorkspace(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		ws, ok := loadWorkspace(c, db)
		if !ok {
			return
		}
		c.JSON(http.StatusOK, ws)
	}
}

func UpdateWorkspace(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		ws, ok := loadWorkspace(c, db)
		if !ok {
			return
		}

		var req struct {
			Name string `json:"name" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		db.Model(ws).Update("name", req.Name)
		c.JSON(http.StatusOK, ws)
	}
}

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
