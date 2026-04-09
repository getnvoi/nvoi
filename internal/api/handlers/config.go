package handlers

import (
	"context"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/getnvoi/nvoi/internal/api"
	"github.com/getnvoi/nvoi/internal/api/config"
	"github.com/getnvoi/nvoi/internal/api/plan"
	pkgcore "github.com/getnvoi/nvoi/pkg/core"
	"gorm.io/gorm"
)

// ── Input / Output types ─────────────────────────────────────────────────────

type PushConfigInput struct {
	RepoScopedInput
	Body struct {
		ComputeProvider api.ComputeProvider `json:"compute_provider" required:"true" enum:"hetzner,aws,scaleway" doc:"Compute provider"`
		DNSProvider     api.DNSProvider     `json:"dns_provider,omitempty" enum:"cloudflare,aws" doc:"DNS provider"`
		StorageProvider api.StorageProvider `json:"storage_provider,omitempty" enum:"cloudflare,aws" doc:"Storage provider"`
		BuildProvider   api.BuildProvider   `json:"build_provider,omitempty" enum:"local,daytona,github" doc:"Build provider"`
		Config          string              `json:"config" required:"true" doc:"YAML config"`
		Env             string              `json:"env,omitempty" doc:"KEY=VALUE pairs (encrypted at rest)"`
	}
}

type PushConfigOutput struct {
	Body api.RepoConfig
}

type GetConfigInput struct {
	RepoScopedInput
	Reveal bool `query:"reveal" default:"false" doc:"Show env values"`
}

type GetConfigOutput struct {
	Body getConfigResponseBody
}

type getConfigResponseBody struct {
	api.RepoConfig
	Env string `json:"env,omitempty"`
}

type ListConfigsInput struct {
	RepoScopedInput
}

type ListConfigsOutput struct {
	Body []configListItem
}

type configListItem struct {
	ID              string              `json:"id"`
	Version         int                 `json:"version"`
	ComputeProvider api.ComputeProvider `json:"compute_provider"`
	DNSProvider     api.DNSProvider     `json:"dns_provider,omitempty"`
	StorageProvider api.StorageProvider `json:"storage_provider,omitempty"`
	BuildProvider   api.BuildProvider   `json:"build_provider,omitempty"`
	Config          string              `json:"config"`
}

type PlanConfigInput struct {
	RepoScopedInput
}

type PlanConfigOutput struct {
	Body planResponseBody
}

type planResponseBody struct {
	Version int         `json:"version"`
	Steps   []plan.Step `json:"steps"`
}

// ── Handlers ─────────────────────────────────────────────────────────────────

func PushConfig(db *gorm.DB) func(context.Context, *PushConfigInput) (*PushConfigOutput, error) {
	return func(ctx context.Context, input *PushConfigInput) (*PushConfigOutput, error) {
		user := api.UserFromContext(ctx)
		repo, err := findRepo(db, user.ID, input.WorkspaceID, input.RepoID)
		if err != nil {
			return nil, err
		}

		// Validate provider enums.
		rc := api.RepoConfig{
			ComputeProvider: input.Body.ComputeProvider,
			DNSProvider:     input.Body.DNSProvider,
			StorageProvider: input.Body.StorageProvider,
			BuildProvider:   input.Body.BuildProvider,
		}
		if err := rc.ValidateProviders(); err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}

		// Parse config.
		cfg, err := config.Parse([]byte(input.Body.Config))
		if err != nil {
			return nil, huma.Error400BadRequest("invalid yaml: " + err.Error())
		}

		env := config.ParseEnv(input.Body.Env)
		resolved, err := plan.ResolveDeploymentSteps(cfg, nil, env)
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}

		errs := config.Validate(resolved.Config)
		if len(errs) > 0 {
			msgs := make([]string, len(errs))
			for i, e := range errs {
				msgs[i] = e.Error()
			}
			return nil, huma.Error400BadRequest(strings.Join(msgs, "; "))
		}

		// Next version number.
		var maxVersion int
		db.Model(&api.RepoConfig{}).
			Where("repo_id = ?", repo.ID).
			Select("COALESCE(MAX(version), 0)").
			Scan(&maxVersion)

		rc.RepoID = repo.ID
		rc.Version = maxVersion + 1
		rc.Config = input.Body.Config
		rc.Env = input.Body.Env
		if err := db.Create(&rc).Error; err != nil {
			return nil, huma.Error500InternalServerError("failed to save config")
		}

		return &PushConfigOutput{Body: rc}, nil
	}
}

func GetConfig(db *gorm.DB) func(context.Context, *GetConfigInput) (*GetConfigOutput, error) {
	return func(ctx context.Context, input *GetConfigInput) (*GetConfigOutput, error) {
		user := api.UserFromContext(ctx)
		repo, err := findRepo(db, user.ID, input.WorkspaceID, input.RepoID)
		if err != nil {
			return nil, err
		}

		var rc api.RepoConfig
		if err := db.Where("repo_id = ?", repo.ID).Order("version DESC").First(&rc).Error; err != nil {
			return nil, huma.Error404NotFound("no config found")
		}

		resp := getConfigResponseBody{RepoConfig: rc}
		if input.Reveal {
			resp.Env = rc.Env
		}

		return &GetConfigOutput{Body: resp}, nil
	}
}

func ListConfigs(db *gorm.DB) func(context.Context, *ListConfigsInput) (*ListConfigsOutput, error) {
	return func(ctx context.Context, input *ListConfigsInput) (*ListConfigsOutput, error) {
		user := api.UserFromContext(ctx)
		repo, err := findRepo(db, user.ID, input.WorkspaceID, input.RepoID)
		if err != nil {
			return nil, err
		}

		var configs []api.RepoConfig
		db.Where("repo_id = ?", repo.ID).Order("version DESC").Find(&configs)

		out := make([]configListItem, len(configs))
		for i, rc := range configs {
			out[i] = configListItem{
				ID: rc.ID, Version: rc.Version,
				ComputeProvider: rc.ComputeProvider,
				DNSProvider:     rc.DNSProvider,
				StorageProvider: rc.StorageProvider,
				BuildProvider:   rc.BuildProvider,
				Config:          rc.Config,
			}
		}

		return &ListConfigsOutput{Body: out}, nil
	}
}

func PlanConfig(db *gorm.DB) func(context.Context, *PlanConfigInput) (*PlanConfigOutput, error) {
	return func(ctx context.Context, input *PlanConfigInput) (*PlanConfigOutput, error) {
		user := api.UserFromContext(ctx)
		repo, err := findRepo(db, user.ID, input.WorkspaceID, input.RepoID)
		if err != nil {
			return nil, err
		}

		var rc api.RepoConfig
		if err := db.Where("repo_id = ?", repo.ID).Order("version DESC").First(&rc).Error; err != nil {
			return nil, huma.Error404NotFound("no config found")
		}

		cfg, err := config.Parse([]byte(rc.Config))
		if err != nil {
			return nil, huma.Error500InternalServerError("corrupt config: " + err.Error())
		}

		env := config.ParseEnv(rc.Env)

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

		resolved, err := plan.ResolveDeploymentSteps(cfg, reality, env)
		if err != nil {
			return nil, huma.Error500InternalServerError("plan failed: " + err.Error())
		}

		return &PlanConfigOutput{Body: planResponseBody{
			Version: rc.Version,
			Steps:   resolved.Steps,
		}}, nil
	}
}

// ── helpers ────────────────────────────────────────────────────────────────────

func stringParam(params map[string]any, key string) string {
	v, _ := params[key].(string)
	return v
}
