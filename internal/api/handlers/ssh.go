package handlers

import (
	"io"
	"net/http"

	pkgcore "github.com/getnvoi/nvoi/pkg/core"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RunSSH runs a command on the master node via SSH and streams output.
//
// POST /workspaces/:workspace_id/repos/:repo_id/ssh
func RunSSH(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		repo, ok := loadRepo(c, db)
		if !ok {
			return
		}

		var req struct {
			Command []string `json:"command" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		cluster, err := clusterFromLatestConfig(db, repo)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		// Stream output as plain text.
		c.Header("Content-Type", "text/plain")
		c.Writer.WriteHeader(http.StatusOK)

		cluster.Output = &streamOutput{w: c.Writer}

		sshErr := pkgcore.SSH(c.Request.Context(), pkgcore.SSHRequest{
			Cluster: *cluster,
			Command: req.Command,
		})
		if sshErr != nil {
			c.Writer.Write([]byte("\nerror: " + sshErr.Error() + "\n"))
		}
	}
}

// streamOutput implements pkgcore.Output, writing directly to the HTTP response.
type streamOutput struct {
	w io.Writer
}

func (s *streamOutput) Command(string, string, string, ...any) {}
func (s *streamOutput) Progress(string)                        {}
func (s *streamOutput) Success(string)                         {}
func (s *streamOutput) Warning(string)                         {}
func (s *streamOutput) Info(string)                            {}
func (s *streamOutput) Error(error)                            {}
func (s *streamOutput) Writer() io.Writer                      { return s.w }
