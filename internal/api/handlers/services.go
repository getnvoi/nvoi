package handlers

import (
	"context"
	"strconv"

	"github.com/danielgtaylor/huma/v2"
	pkgcore "github.com/getnvoi/nvoi/pkg/core"
	"gorm.io/gorm"
)

type ServiceLogsInput struct {
	RepoScopedInput
	Service    string `path:"service" doc:"Service name"`
	Follow     string `query:"follow" default:"false" doc:"Follow log output"`
	Tail       string `query:"tail" default:"50" doc:"Lines from end"`
	Since      string `query:"since" doc:"Show logs since duration (e.g. 5m)"`
	Previous   string `query:"previous" default:"false" doc:"Previous container logs"`
	Timestamps string `query:"timestamps" default:"false" doc:"Include timestamps"`
}

type ExecInput struct {
	RepoScopedInput
	Service string `path:"service" doc:"Service name"`
	Body    struct {
		Command []string `json:"command" required:"true" doc:"Command to run"`
	}
}

func ServiceLogs(db *gorm.DB) func(context.Context, *ServiceLogsInput) (*huma.StreamResponse, error) {
	return func(ctx context.Context, input *ServiceLogsInput) (*huma.StreamResponse, error) {
		cluster, err := repoCluster(ctx, db, input.RepoScopedInput)
		if err != nil {
			return nil, err
		}

		tail, _ := strconv.Atoi(input.Tail)
		if tail == 0 {
			tail = 50
		}

		return &huma.StreamResponse{
			Body: func(ctx huma.Context) {
				ctx.SetHeader("Content-Type", "text/plain")
				cluster.Output = &streamOutput{w: ctx.BodyWriter()}

				logsErr := pkgcore.Logs(ctx.Context(), pkgcore.LogsRequest{
					Cluster:    *cluster,
					Service:    input.Service,
					Follow:     input.Follow == "true",
					Tail:       tail,
					Since:      input.Since,
					Previous:   input.Previous == "true",
					Timestamps: input.Timestamps == "true",
				})
				if logsErr != nil {
					ctx.BodyWriter().Write([]byte("\nerror: " + logsErr.Error() + "\n"))
				}
			},
		}, nil
	}
}

func ExecCommand(db *gorm.DB) func(context.Context, *ExecInput) (*huma.StreamResponse, error) {
	return func(ctx context.Context, input *ExecInput) (*huma.StreamResponse, error) {
		cluster, err := repoCluster(ctx, db, input.RepoScopedInput)
		if err != nil {
			return nil, err
		}

		return &huma.StreamResponse{
			Body: func(ctx huma.Context) {
				ctx.SetHeader("Content-Type", "text/plain")
				cluster.Output = &streamOutput{w: ctx.BodyWriter()}

				execErr := pkgcore.Exec(ctx.Context(), pkgcore.ExecRequest{
					Cluster: *cluster,
					Service: input.Service,
					Command: input.Body.Command,
				})
				if execErr != nil {
					ctx.BodyWriter().Write([]byte("\nerror: " + execErr.Error() + "\n"))
				}
			},
		}, nil
	}
}
