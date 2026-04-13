package handlers

import (
	"context"
	"regexp"

	"github.com/danielgtaylor/huma/v2"
	"github.com/getnvoi/nvoi/internal/api"
	"gorm.io/gorm"
)

// validAlias matches lowercase alphanumeric slugs with hyphens (e.g. hetzner-prod, cf-dns).
// No uppercase, no spaces, no special characters. Must start and end with alphanumeric.
var validAlias = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ── Inputs / Outputs ────────────────────────────────────────────────────────

type ListProvidersInput struct {
	WorkspaceScopedInput
}

type ListProvidersOutput struct {
	Body []providerItem
}

type providerItem struct {
	ID       string `json:"id"`
	Alias    string `json:"alias"`
	Kind     string `json:"kind"`
	Provider string `json:"provider"`
}

type SetProviderInput struct {
	WorkspaceScopedInput
	Body struct {
		Alias       string           `json:"alias" required:"true" doc:"User-chosen name (e.g. hetzner-prod, cf-dns)"`
		Kind        api.ProviderKind `json:"kind" required:"true" enum:"compute,dns,storage,build" doc:"Provider domain"`
		Provider    string           `json:"provider" required:"true" doc:"Provider implementation (hetzner, cloudflare, aws, daytona, github)"`
		Credentials string           `json:"credentials" required:"true" doc:"Encrypted JSON credentials"`
	}
}

type setProviderResult struct {
	ID       string `json:"id"`
	Alias    string `json:"alias"`
	Kind     string `json:"kind"`
	Provider string `json:"provider"`
	Created  bool   `json:"created"`
}

type SetProviderOutput struct {
	Body setProviderResult
}

type DeleteProviderInput struct {
	WorkspaceScopedInput
	Alias string `path:"alias" required:"true" doc:"Provider alias"`
}

type DeleteProviderOutput struct {
	Body struct {
		Status string `json:"status"`
	}
}

// ── Handlers ────────────────────────────────────────────────────────────────

func ListProviders(db *gorm.DB) func(context.Context, *ListProvidersInput) (*ListProvidersOutput, error) {
	return func(ctx context.Context, input *ListProvidersInput) (*ListProvidersOutput, error) {
		user := api.UserFromContext(ctx)
		ws, err := findWorkspace(db, user.ID, input.WorkspaceID)
		if err != nil {
			return nil, err
		}

		var providers []api.InfraProvider
		if err := db.Where("workspace_id = ?", ws.ID).Order("kind, alias").Find(&providers).Error; err != nil {
			return nil, huma.Error500InternalServerError(err.Error())
		}

		items := make([]providerItem, len(providers))
		for i, p := range providers {
			items[i] = providerItem{ID: p.ID, Alias: p.Alias, Kind: string(p.Kind), Provider: p.Provider}
		}
		return &ListProvidersOutput{Body: items}, nil
	}
}

func SetProvider(db *gorm.DB) func(context.Context, *SetProviderInput) (*SetProviderOutput, error) {
	return func(ctx context.Context, input *SetProviderInput) (*SetProviderOutput, error) {
		user := api.UserFromContext(ctx)
		ws, err := findWorkspace(db, user.ID, input.WorkspaceID)
		if err != nil {
			return nil, err
		}

		// Validate alias format: lowercase slug only.
		if !validAlias.MatchString(input.Body.Alias) {
			return nil, huma.Error400BadRequest("invalid alias — must be lowercase alphanumeric with hyphens (e.g. hetzner-prod, cf-dns)")
		}

		// local build provider requires Docker on the client machine — not available server-side.
		if input.Body.Kind == api.ProviderKindBuild && input.Body.Provider == "local" {
			return nil, huma.Error400BadRequest("build provider 'local' is not supported in cloud mode — use 'daytona' or 'github'")
		}

		// Upsert by alias within workspace.
		var existing api.InfraProvider
		result := db.Where("workspace_id = ? AND alias = ?", ws.ID, input.Body.Alias).First(&existing)

		if result.Error == nil {
			// Update: allow changing kind, provider, and credentials.
			existing.Kind = input.Body.Kind
			existing.Provider = input.Body.Provider
			existing.Credentials = input.Body.Credentials
			if err := db.Save(&existing).Error; err != nil {
				return nil, huma.Error500InternalServerError(err.Error())
			}
			return &SetProviderOutput{Body: setProviderResult{
				ID: existing.ID, Alias: existing.Alias,
				Kind: string(existing.Kind), Provider: existing.Provider,
				Created: false,
			}}, nil
		}

		// Create new.
		prov := api.InfraProvider{
			WorkspaceID: ws.ID,
			Alias:       input.Body.Alias,
			Kind:        input.Body.Kind,
			Provider:    input.Body.Provider,
			Credentials: input.Body.Credentials,
		}
		if err := db.Create(&prov).Error; err != nil {
			return nil, huma.Error500InternalServerError(err.Error())
		}
		return &SetProviderOutput{Body: setProviderResult{
			ID: prov.ID, Alias: prov.Alias,
			Kind: string(prov.Kind), Provider: prov.Provider,
			Created: true,
		}}, nil
	}
}

func DeleteProvider(db *gorm.DB) func(context.Context, *DeleteProviderInput) (*DeleteProviderOutput, error) {
	return func(ctx context.Context, input *DeleteProviderInput) (*DeleteProviderOutput, error) {
		user := api.UserFromContext(ctx)
		ws, err := findWorkspace(db, user.ID, input.WorkspaceID)
		if err != nil {
			return nil, err
		}

		result := db.Where("workspace_id = ? AND alias = ?", ws.ID, input.Alias).Delete(&api.InfraProvider{})
		if result.RowsAffected == 0 {
			return nil, huma.Error404NotFound("provider not found")
		}

		return &DeleteProviderOutput{Body: struct {
			Status string `json:"status"`
		}{Status: "deleted"}}, nil
	}
}
