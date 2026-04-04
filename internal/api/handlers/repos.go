package handlers

import (
	"net/http"

	"github.com/getnvoi/nvoi/internal/api"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

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

func CreateRepo(db *gorm.DB) gin.HandlerFunc {
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

func GetRepo(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		repo, ok := loadRepo(c, db)
		if !ok {
			return
		}
		c.JSON(http.StatusOK, repo)
	}
}

func UpdateRepo(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		repo, ok := loadRepo(c, db)
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

		db.Model(repo).Update("name", req.Name)
		c.JSON(http.StatusOK, repo)
	}
}

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
