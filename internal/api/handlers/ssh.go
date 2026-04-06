package handlers

import (
	"context"
	"io"

	"github.com/danielgtaylor/huma/v2"
	"github.com/getnvoi/nvoi/internal/api"
	pkgcore "github.com/getnvoi/nvoi/pkg/core"
	"gorm.io/gorm"
)

// ── Input / Output types ─────────────────────────────────────────────────────

type RunSSHInput struct {
	RepoScopedInput
	Body struct {
		Command []string `json:"command" required:"true" doc:"Command to run on master node"`
	}
}

// ── Handler ──────────────────────────────────────────────────────────────────

func RunSSH(db *gorm.DB) func(context.Context, *RunSSHInput) (*huma.StreamResponse, error) {
	return func(ctx context.Context, input *RunSSHInput) (*huma.StreamResponse, error) {
		user := api.UserFromContext(ctx)
		repo, err := findRepo(db, user.ID, input.WorkspaceID, input.RepoID)
		if err != nil {
			return nil, err
		}

		cluster, err := clusterFromLatestConfig(db, repo)
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}

		return &huma.StreamResponse{
			Body: func(ctx huma.Context) {
				ctx.SetHeader("Content-Type", "text/plain")
				cluster.Output = &streamOutput{w: ctx.BodyWriter()}

				sshErr := pkgcore.SSH(ctx.Context(), pkgcore.SSHRequest{
					Cluster: *cluster,
					Command: input.Body.Command,
				})
				if sshErr != nil {
					ctx.BodyWriter().Write([]byte("\nerror: " + sshErr.Error() + "\n"))
				}
			},
		}, nil
	}
}

// streamOutput implements pkgcore.Output, writing directly to a writer.
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
