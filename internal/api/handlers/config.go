package handlers

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/getnvoi/nvoi/internal/api"
	"github.com/getnvoi/nvoi/internal/api/config"
	"github.com/getnvoi/nvoi/internal/api/managed"
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

		// Load stored managed service credentials for this repo.
		storedCreds := loadManagedCreds(db, repo.ID)

		// Expand managed services (replace with real specs, inject creds, add volumes).
		expanded, newCreds, err := managed.Expand(cfg, storedCreds)
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}

		// Validate the expanded config (after managed services are resolved).
		errs := config.Validate(expanded)
		if len(errs) > 0 {
			msgs := make([]string, len(errs))
			for i, e := range errs {
				msgs[i] = e.Error()
			}
			return nil, huma.Error400BadRequest(strings.Join(msgs, "; "))
		}

		// Merge managed service credential secrets into env for plan validation.
		env := config.ParseEnv(input.Body.Env)
		for k, v := range managed.CredentialSecrets(mergeCreds(storedCreds, newCreds), cfg) {
			env[k] = v
		}

		// Validate that the plan can be built (env references resolve).
		if _, err := plan.Build(nil, expanded, env); err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}

		// Persist new managed service credentials.
		for name, creds := range newCreds {
			svc := cfg.Services[name]
			credsJSON, _ := json.Marshal(creds)
			if err := db.Create(&api.RepoManagedServiceConfig{
				RepoID:      repo.ID,
				Name:        name,
				Kind:        svc.Managed,
				Credentials: string(credsJSON),
			}).Error; err != nil {
				return nil, huma.Error500InternalServerError("failed to save managed credentials")
			}
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

		return &PlanConfigOutput{Body: planResponseBody{
			Version: rc.Version,
			Steps:   steps,
		}}, nil
	}
}

// ── helpers ────────────────────────────────────────────────────────────────────

func loadManagedCreds(db *gorm.DB, repoID string) map[string]map[string]string {
	var rows []api.RepoManagedServiceConfig
	db.Where("repo_id = ?", repoID).Find(&rows)

	creds := make(map[string]map[string]string, len(rows))
	for _, row := range rows {
		var m map[string]string
		if err := json.Unmarshal([]byte(row.Credentials), &m); err != nil {
			continue
		}
		creds[row.Name] = m
	}
	return creds
}

func mergeCreds(stored, newCreds map[string]map[string]string) map[string]map[string]string {
	merged := make(map[string]map[string]string, len(stored)+len(newCreds))
	for k, v := range stored {
		merged[k] = v
	}
	for k, v := range newCreds {
		merged[k] = v
	}
	return merged
}
