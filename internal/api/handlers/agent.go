// Package handlers implements the HTTP API endpoints using huma and Gin.
package handlers

import (
	"context"
	"strconv"

	"github.com/danielgtaylor/huma/v2"
	pkgcore "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/managed"
	"gorm.io/gorm"
)

// ── Input / Output types ─────────────────────────────────────────────────────

type AgentListInput struct{ RepoScopedInput }
type AgentListOutput struct{ Body []pkgcore.ManagedService }

type AgentExecInput struct {
	RepoScopedInput
	Name string `path:"name" doc:"Agent service name"`
	Body struct {
		Command []string `json:"command" required:"true" doc:"Command to run"`
	}
}

type AgentLogsInput struct {
	RepoScopedInput
	Name       string `path:"name" doc:"Agent service name"`
	Follow     string `query:"follow" default:"false" doc:"Follow log output"`
	Tail       string `query:"tail" default:"50" doc:"Number of lines"`
	Since      string `query:"since" doc:"Since duration (5m, 1h)"`
	Previous   string `query:"previous" default:"false" doc:"Previous container"`
	Timestamps string `query:"timestamps" default:"false" doc:"Show timestamps"`
}

// ── Handlers ─────────────────────────────────────────────────────────────────

func AgentList(db *gorm.DB) func(context.Context, *AgentListInput) (*AgentListOutput, error) {
	return func(ctx context.Context, input *AgentListInput) (*AgentListOutput, error) {
		cluster, err := repoCluster(ctx, db, input.RepoScopedInput)
		if err != nil {
			return nil, err
		}

		var all []pkgcore.ManagedService
		for _, kind := range managed.KindsForCategory("agent") {
			services, err := pkgcore.ManagedList(ctx, pkgcore.ManagedListRequest{
				Cluster: *cluster,
				Kind:    kind,
			})
			if err != nil {
				return nil, huma.Error500InternalServerError(err.Error())
			}
			all = append(all, services...)
		}

		return &AgentListOutput{Body: all}, nil
	}
}

func AgentExec(db *gorm.DB) func(context.Context, *AgentExecInput) (*huma.StreamResponse, error) {
	return func(ctx context.Context, input *AgentExecInput) (*huma.StreamResponse, error) {
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
					Service: input.Name,
					Command: input.Body.Command,
				})
				if execErr != nil {
					ctx.BodyWriter().Write([]byte("\nerror: " + execErr.Error() + "\n"))
				}
			},
		}, nil
	}
}

func AgentLogs(db *gorm.DB) func(context.Context, *AgentLogsInput) (*huma.StreamResponse, error) {
	return func(ctx context.Context, input *AgentLogsInput) (*huma.StreamResponse, error) {
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
					Service:    input.Name,
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
