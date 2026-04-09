package handlers

import (
	"context"

	"github.com/danielgtaylor/huma/v2"
	"github.com/getnvoi/nvoi/internal/api"
	"gorm.io/gorm"
)

// ── Inputs / Outputs ────────────────────────────────────────────────────────

type ListProvidersInput struct {
	WorkspaceScopedInput
}

type ListProvidersOutput struct {
	Body []providerItem
}

type providerItem struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Name string `json:"name"`
}

type SetProviderInput struct {
	WorkspaceScopedInput
	Body struct {
		Kind        api.ProviderKind `json:"kind" required:"true" enum:"compute,dns,storage,build" doc:"Provider kind"`
		Name        string           `json:"name" required:"true" doc:"Provider name (hetzner, cloudflare, aws, etc.)"`
		Credentials string           `json:"credentials" required:"true" doc:"Encrypted JSON credentials"`
	}
}

type SetProviderOutput struct {
	Body api.InfraProvider
}

type DeleteProviderInput struct {
	WorkspaceScopedInput
	Kind string `path:"kind" required:"true" doc:"Provider kind"`
	Name string `path:"name" required:"true" doc:"Provider name"`
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
		if err := db.Where("workspace_id = ?", ws.ID).Order("kind, name").Find(&providers).Error; err != nil {
			return nil, huma.Error500InternalServerError(err.Error())
		}

		items := make([]providerItem, len(providers))
		for i, p := range providers {
			items[i] = providerItem{ID: p.ID, Kind: string(p.Kind), Name: p.Name}
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

		// Upsert: find existing or create new.
		var existing api.InfraProvider
		result := db.Where("workspace_id = ? AND kind = ? AND name = ?", ws.ID, input.Body.Kind, input.Body.Name).First(&existing)

		if result.Error == nil {
			// Update credentials.
			existing.Credentials = input.Body.Credentials
			if err := db.Save(&existing).Error; err != nil {
				return nil, huma.Error500InternalServerError(err.Error())
			}
			return &SetProviderOutput{Body: existing}, nil
		}

		// Create new.
		provider := api.InfraProvider{
			WorkspaceID: ws.ID,
			Kind:        input.Body.Kind,
			Name:        input.Body.Name,
			Credentials: input.Body.Credentials,
		}
		if err := db.Create(&provider).Error; err != nil {
			return nil, huma.Error500InternalServerError(err.Error())
		}
		return &SetProviderOutput{Body: provider}, nil
	}
}

func DeleteProvider(db *gorm.DB) func(context.Context, *DeleteProviderInput) (*DeleteProviderOutput, error) {
	return func(ctx context.Context, input *DeleteProviderInput) (*DeleteProviderOutput, error) {
		user := api.UserFromContext(ctx)
		ws, err := findWorkspace(db, user.ID, input.WorkspaceID)
		if err != nil {
			return nil, err
		}

		result := db.Where("workspace_id = ? AND kind = ? AND name = ?", ws.ID, input.Kind, input.Name).Delete(&api.InfraProvider{})
		if result.RowsAffected == 0 {
			return nil, huma.Error404NotFound("provider not found")
		}

		return &DeleteProviderOutput{Body: struct {
			Status string `json:"status"`
		}{Status: "deleted"}}, nil
	}
}
