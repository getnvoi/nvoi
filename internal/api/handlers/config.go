package handlers

import (
	"context"
	"fmt"
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
		ComputeProvider api.ComputeProvider `json:"compute_provider,omitempty" enum:"hetzner,aws,scaleway" doc:"Compute provider (optional if repo has InfraProvider links)"`
		DNSProvider     api.DNSProvider     `json:"dns_provider,omitempty" enum:"cloudflare,aws" doc:"DNS provider"`
		StorageProvider api.StorageProvider `json:"storage_provider,omitempty" enum:"cloudflare,aws" doc:"Storage provider"`
		BuildProvider   api.BuildProvider   `json:"build_provider,omitempty" enum:"local,daytona,github" doc:"Build provider"`
		Config          string              `json:"config" required:"true" doc:"YAML config"`
		Env             string              `json:"env,omitempty" doc:"KEY=VALUE pairs (encrypted at rest)"`
		BaseVersion     int                 `json:"base_version,omitempty" doc:"Expected current version (0 = skip check)"`
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

// PushConfig stores a config version. Full replace — no merging.
// Provider fields optional if repo has InfraProvider links.
func PushConfig(db *gorm.DB) func(context.Context, *PushConfigInput) (*PushConfigOutput, error) {
	return func(ctx context.Context, input *PushConfigInput) (*PushConfigOutput, error) {
		user := api.UserFromContext(ctx)
		repo, err := findRepo(db, user.ID, input.WorkspaceID, input.RepoID)
		if err != nil {
			return nil, err
		}

		// Providers: body takes priority, fall back to InfraProvider FKs.
		compute := input.Body.ComputeProvider
		dns := input.Body.DNSProvider
		storage := input.Body.StorageProvider
		build := input.Body.BuildProvider
		if compute == "" && repo.ComputeProvider != nil {
			compute = api.ComputeProvider(repo.ComputeProvider.Name)
		}
		if dns == "" && repo.DNSProvider != nil {
			dns = api.DNSProvider(repo.DNSProvider.Name)
		}
		if storage == "" && repo.StorageProvider != nil {
			storage = api.StorageProvider(repo.StorageProvider.Name)
		}
		if build == "" && repo.BuildProvider != nil {
			build = api.BuildProvider(repo.BuildProvider.Name)
		}

		rc := api.RepoConfig{
			ComputeProvider: compute,
			DNSProvider:     dns,
			StorageProvider: storage,
			BuildProvider:   build,
		}
		if err := rc.ValidateProviders(); err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}

		cfg, err := config.Parse([]byte(input.Body.Config))
		if err != nil {
			return nil, huma.Error400BadRequest("invalid yaml: " + err.Error())
		}

		env, err := config.ParseEnv(input.Body.Env)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid env: " + err.Error())
		}
		resolved, err := plan.ResolveDeploymentSteps(cfg, nil, env)
		if err != nil {
			if isMissingCredential(err) {
				return nil, huma.Error400BadRequest(err.Error())
			}
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

		var maxVersion int
		db.Model(&api.RepoConfig{}).
			Where("repo_id = ?", repo.ID).
			Select("COALESCE(MAX(version), 0)").
			Scan(&maxVersion)

		if input.Body.BaseVersion > 0 && input.Body.BaseVersion != maxVersion {
			return nil, huma.Error409Conflict(fmt.Sprintf(
				"config version conflict: you read v%d but current is v%d — reload and retry",
				input.Body.BaseVersion, maxVersion,
			))
		}

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

		env, err := config.ParseEnv(rc.Env)
		if err != nil {
			return nil, huma.Error500InternalServerError("corrupt env: " + err.Error())
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

		resolved, err := plan.ResolveDeploymentSteps(cfg, reality, env)
		if err != nil {
			if isMissingCredential(err) {
				return nil, huma.Error400BadRequest(err.Error())
			}
			return nil, huma.Error500InternalServerError("plan failed: " + err.Error())
		}

		return &PlanConfigOutput{Body: planResponseBody{
			Version: rc.Version,
			Steps:   resolved.Steps,
		}}, nil
	}
}
