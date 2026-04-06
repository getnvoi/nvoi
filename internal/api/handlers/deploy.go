package handlers

import (
	"context"
	"encoding/json"

	"github.com/danielgtaylor/huma/v2"
	"github.com/getnvoi/nvoi/internal/api"
	"github.com/getnvoi/nvoi/internal/api/config"
	"github.com/getnvoi/nvoi/internal/api/managed"
	"github.com/getnvoi/nvoi/internal/api/plan"
	pkgcore "github.com/getnvoi/nvoi/pkg/core"
	"gorm.io/gorm"
)

// ── Input / Output types ─────────────────────────────────────────────────────

type DeployInput struct {
	RepoScopedInput
}

type DeployOutput struct {
	Body api.Deployment
}

type ListDeploymentsInput struct {
	RepoScopedInput
}

type ListDeploymentsOutput struct {
	Body []api.Deployment
}

type GetDeploymentInput struct {
	RepoScopedInput
	DeploymentID string `path:"deployment_id" format:"uuid" doc:"Deployment ID"`
}

type GetDeploymentOutput struct {
	Body api.Deployment
}

type RunDeploymentInput struct {
	RepoScopedInput
	DeploymentID string `path:"deployment_id" format:"uuid" doc:"Deployment ID"`
}

type RunDeploymentOutput struct {
	Body struct {
		Status string `json:"status"`
	}
}

type DeploymentLogsInput struct {
	RepoScopedInput
	DeploymentID string `path:"deployment_id" format:"uuid" doc:"Deployment ID"`
}

// ── Handlers ─────────────────────────────────────────────────────────────────

func Deploy(db *gorm.DB) func(context.Context, *DeployInput) (*DeployOutput, error) {
	return func(ctx context.Context, input *DeployInput) (*DeployOutput, error) {
		user := api.UserFromContext(ctx)
		repo, err := findRepo(db, user.ID, input.WorkspaceID, input.RepoID)
		if err != nil {
			return nil, err
		}

		var rc api.RepoConfig
		if err := db.Where("repo_id = ?", repo.ID).Order("version DESC").First(&rc).Error; err != nil {
			return nil, huma.Error400BadRequest("no config found — push a config first")
		}

		cfg, err := config.Parse([]byte(rc.Config))
		if err != nil {
			return nil, huma.Error500InternalServerError("corrupt config: " + err.Error())
		}

		storedCreds := loadManagedCreds(db, repo.ID)
		expanded, _, err := managed.Expand(cfg, storedCreds)
		if err != nil {
			return nil, huma.Error500InternalServerError("expand failed: " + err.Error())
		}

		env := config.ParseEnv(rc.Env)
		for k, v := range managed.CredentialSecrets(storedCreds, cfg) {
			env[k] = v
		}

		// Query reality — what's actually deployed.
		creds, credErr := resolveAllCredentials(&rc, env)
		var reality *config.Config
		if credErr == nil {
			reality = plan.InfraState(ctx, plan.InfraStateRequest{
				Cluster: pkgcore.Cluster{
					AppName:     repo.Name,
					Env:         repo.Environment,
					Provider:    string(rc.ComputeProvider),
					Credentials: creds.Compute,
					SSHKey:      []byte(repo.SSHPrivateKey),
				},
				DNS:     pkgcore.ProviderRef{Name: string(rc.DNSProvider), Creds: creds.DNS},
				Storage: pkgcore.ProviderRef{Name: string(rc.StorageProvider), Creds: creds.Storage},
			})
		}

		steps, err := plan.Build(reality, expanded, env)
		if err != nil {
			return nil, huma.Error500InternalServerError("plan failed: " + err.Error())
		}

		var deployment api.Deployment
		err = db.Transaction(func(tx *gorm.DB) error {
			deployment = api.Deployment{
				RepoID:       repo.ID,
				RepoConfigID: rc.ID,
				Status:       api.DeploymentPending,
			}
			if err := tx.Create(&deployment).Error; err != nil {
				return err
			}

			for i, step := range steps {
				paramsJSON, _ := json.Marshal(step.Params)
				if err := tx.Create(&api.DeploymentStep{
					DeploymentID: deployment.ID,
					Position:     i + 1,
					Kind:         string(step.Kind),
					Name:         step.Name,
					Params:       string(paramsJSON),
					Status:       api.StepStatusPending,
				}).Error; err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to create deployment")
		}

		db.Preload("Steps", func(db *gorm.DB) *gorm.DB {
			return db.Order("position")
		}).First(&deployment, "id = ?", deployment.ID)

		return &DeployOutput{Body: deployment}, nil
	}
}

func RunDeployment(db *gorm.DB) func(context.Context, *RunDeploymentInput) (*RunDeploymentOutput, error) {
	return func(ctx context.Context, input *RunDeploymentInput) (*RunDeploymentOutput, error) {
		user := api.UserFromContext(ctx)
		repo, err := findRepo(db, user.ID, input.WorkspaceID, input.RepoID)
		if err != nil {
			return nil, err
		}

		var deployment api.Deployment
		if err := db.Where("id = ? AND repo_id = ?", input.DeploymentID, repo.ID).First(&deployment).Error; err != nil {
			return nil, huma.Error404NotFound("deployment not found")
		}

		if deployment.Status != api.DeploymentPending {
			return nil, huma.Error400BadRequest("deployment is not pending")
		}

		var rc api.RepoConfig
		if err := db.First(&rc, "id = ?", deployment.RepoConfigID).Error; err != nil {
			return nil, huma.Error500InternalServerError("config not found")
		}

		env := config.ParseEnv(rc.Env)

		go Execute(context.Background(), db, ExecuteParams{
			Deployment: &deployment,
			Repo:       repo,
			Config:     &rc,
			Env:        env,
			GitToken:   user.GithubToken,
		})

		return &RunDeploymentOutput{Body: struct {
			Status string `json:"status"`
		}{Status: "running"}}, nil
	}
}

func GetDeployment(db *gorm.DB) func(context.Context, *GetDeploymentInput) (*GetDeploymentOutput, error) {
	return func(ctx context.Context, input *GetDeploymentInput) (*GetDeploymentOutput, error) {
		user := api.UserFromContext(ctx)
		repo, err := findRepo(db, user.ID, input.WorkspaceID, input.RepoID)
		if err != nil {
			return nil, err
		}

		var deployment api.Deployment
		result := db.
			Preload("Steps", func(db *gorm.DB) *gorm.DB {
				return db.Order("position")
			}).
			Preload("Steps.Logs").
			Where("id = ? AND repo_id = ?", input.DeploymentID, repo.ID).
			First(&deployment)

		if result.Error != nil {
			return nil, huma.Error404NotFound("deployment not found")
		}

		return &GetDeploymentOutput{Body: deployment}, nil
	}
}

func ListDeployments(db *gorm.DB) func(context.Context, *ListDeploymentsInput) (*ListDeploymentsOutput, error) {
	return func(ctx context.Context, input *ListDeploymentsInput) (*ListDeploymentsOutput, error) {
		user := api.UserFromContext(ctx)
		repo, err := findRepo(db, user.ID, input.WorkspaceID, input.RepoID)
		if err != nil {
			return nil, err
		}

		var deployments []api.Deployment
		db.Where("repo_id = ?", repo.ID).Order("created_at DESC").Find(&deployments)
		return &ListDeploymentsOutput{Body: deployments}, nil
	}
}

func DeploymentLogs(db *gorm.DB) func(context.Context, *DeploymentLogsInput) (*huma.StreamResponse, error) {
	return func(ctx context.Context, input *DeploymentLogsInput) (*huma.StreamResponse, error) {
		user := api.UserFromContext(ctx)
		repo, err := findRepo(db, user.ID, input.WorkspaceID, input.RepoID)
		if err != nil {
			return nil, err
		}

		var deployment api.Deployment
		if err := db.Where("id = ? AND repo_id = ?", input.DeploymentID, repo.ID).First(&deployment).Error; err != nil {
			return nil, huma.Error404NotFound("deployment not found")
		}

		var steps []api.DeploymentStep
		db.Where("deployment_id = ?", deployment.ID).
			Order("position").
			Preload("Logs").
			Find(&steps)

		return &huma.StreamResponse{
			Body: func(ctx huma.Context) {
				ctx.SetHeader("Content-Type", "application/x-ndjson")
				for _, step := range steps {
					for _, log := range step.Logs {
						ctx.BodyWriter().Write([]byte(log.Line))
						ctx.BodyWriter().Write([]byte("\n"))
					}
				}
			},
		}, nil
	}
}
