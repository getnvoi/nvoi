package handlers

import (
	"context"
	"fmt"

	"github.com/danielgtaylor/huma/v2"
	"github.com/getnvoi/nvoi/internal/api"
	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/internal/reconcile"
	"gorm.io/gorm"
)

// ── Show ────────────────────────────────────────────────────────────────────

type ConfigShowInput struct {
	RepoScopedInput
}

type ConfigOutput struct {
	Body struct {
		Config   string   `json:"config" doc:"Raw YAML config"`
		Warnings []string `json:"warnings,omitempty" doc:"Validation warnings"`
	}
}

func ConfigShow(db *gorm.DB) func(context.Context, *ConfigShowInput) (*ConfigOutput, error) {
	return func(ctx context.Context, input *ConfigShowInput) (*ConfigOutput, error) {
		user := api.UserFromContext(ctx)
		repo, err := findRepo(db, user.ID, input.WorkspaceID, input.RepoID)
		if err != nil {
			return nil, err
		}
		out := &ConfigOutput{}
		out.Body.Config = repo.Config
		out.Body.Warnings = validateStored(repo)
		return out, nil
	}
}

// ── Save ────────────────────────────────────────────────────────────────────
// Always saves. Validates after. Returns config + warnings.
// Deploy is the hard gate — not save.

type ConfigSaveInput struct {
	RepoScopedInput
	Body struct {
		Config string `json:"config" required:"true" doc:"Full YAML config"`
	}
}

func ConfigSave(db *gorm.DB) func(context.Context, *ConfigSaveInput) (*ConfigOutput, error) {
	return func(ctx context.Context, input *ConfigSaveInput) (*ConfigOutput, error) {
		user := api.UserFromContext(ctx)
		repo, err := findRepo(db, user.ID, input.WorkspaceID, input.RepoID)
		if err != nil {
			return nil, err
		}

		// Parse — hard reject only if YAML is malformed.
		cfg, err := config.ParseAppConfig([]byte(input.Body.Config))
		if err != nil {
			return nil, huma.Error400BadRequest(fmt.Sprintf("invalid YAML: %v", err))
		}

		// Inject identity from repo.
		cfg.App = repo.Name
		cfg.Env = repo.Environment

		// Serialize back.
		data, err := config.MarshalAppConfig(cfg)
		if err != nil {
			return nil, huma.Error500InternalServerError(fmt.Sprintf("serialize config: %v", err))
		}
		yaml := string(data)

		// Save — always.
		if err := db.Model(repo).Update("config", yaml).Error; err != nil {
			return nil, err
		}

		// Validate — warn, don't reject.
		out := &ConfigOutput{}
		out.Body.Config = yaml
		if err := reconcile.ValidateConfig(cfg); err != nil {
			out.Body.Warnings = []string{err.Error()}
		}

		return out, nil
	}
}

// validateStored parses and validates the stored config, returns warnings.
func validateStored(repo *api.Repo) []string {
	if repo.Config == "" {
		return nil
	}
	cfg, err := config.ParseAppConfig([]byte(repo.Config))
	if err != nil {
		return []string{err.Error()}
	}
	cfg.App = repo.Name
	cfg.Env = repo.Environment
	if err := reconcile.ValidateConfig(cfg); err != nil {
		return []string{err.Error()}
	}
	return nil
}
