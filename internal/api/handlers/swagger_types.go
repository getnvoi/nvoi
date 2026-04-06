package handlers

import "github.com/getnvoi/nvoi/internal/api"

// ── Request types ────────────────────────────────────────────────────────────

// createWorkspaceRequest is the body for POST /workspaces.
type createWorkspaceRequest struct {
	Name string `json:"name" binding:"required" example:"production"`
}

// updateWorkspaceRequest is the body for PUT /workspaces/:workspace_id.
type updateWorkspaceRequest struct {
	Name string `json:"name" binding:"required" example:"staging"`
}

// createRepoRequest is the body for POST /workspaces/:workspace_id/repos.
type createRepoRequest struct {
	Name string `json:"name" binding:"required" example:"my-app"`
}

// updateRepoRequest is the body for PUT /workspaces/:workspace_id/repos/:repo_id.
type updateRepoRequest struct {
	Name string `json:"name" binding:"required" example:"my-app-v2"`
}

// pushConfigRequest is the body for POST /workspaces/:workspace_id/repos/:repo_id/config.
type pushConfigRequest struct {
	ComputeProvider api.ComputeProvider `json:"compute_provider" binding:"required" example:"hetzner" enums:"hetzner,aws,scaleway"`
	DNSProvider     api.DNSProvider     `json:"dns_provider,omitempty" example:"cloudflare" enums:"cloudflare,aws"`
	StorageProvider api.StorageProvider `json:"storage_provider,omitempty" example:"cloudflare" enums:"cloudflare,aws"`
	BuildProvider   api.BuildProvider   `json:"build_provider,omitempty" example:"github" enums:"local,daytona,github"`
	Config          string              `json:"config" binding:"required" example:"servers:\n  master:\n    type: cx23\n    region: fsn1"`
	Env             string              `json:"env,omitempty" example:"HETZNER_TOKEN=xxx\nRAILS_MASTER_KEY=yyy"`
}

// runSSHRequest is the body for POST /workspaces/:workspace_id/repos/:repo_id/ssh.
type runSSHRequest struct {
	Command []string `json:"command" binding:"required" example:"kubectl,get,pods"`
}

// ── Response types ───────────────────────────────────────────────────────────

// errorResponse is the standard error envelope.
type errorResponse struct {
	Error string `json:"error" example:"workspace not found"`
}

// validationErrorResponse is returned when config validation fails.
type validationErrorResponse struct {
	Errors []string `json:"errors" example:"service \"web\": image or build required"`
}

// deleteResponse is returned by DELETE endpoints.
type deleteResponse struct {
	Deleted bool   `json:"deleted" example:"true"`
	Name    string `json:"name,omitempty" example:"production"`
}

// statusResponse is returned when an async operation starts.
type statusResponse struct {
	Status string `json:"status" example:"running"`
}

// healthResponse is returned by GET /health.
type healthResponse struct {
	Status string `json:"status" example:"ok"`
}

// planResponse is returned by GET /config/plan.
type planResponse struct {
	Version int `json:"version" example:"3"`
	// Steps is the ordered deployment sequence.
	Steps []planStep `json:"steps"`
}

// planStep is a single action in a deployment plan.
type planStep struct {
	Kind   string         `json:"kind" example:"instance.set"`
	Name   string         `json:"name" example:"master"`
	Params map[string]any `json:"params,omitempty"`
}

// configListItem is a config version without the env field.
type configListItem struct {
	ID              string              `json:"id" example:"550e8400-e29b-41d4-a716-446655440000"`
	Version         int                 `json:"version" example:"1"`
	ComputeProvider api.ComputeProvider `json:"compute_provider" example:"hetzner"`
	DNSProvider     api.DNSProvider     `json:"dns_provider,omitempty" example:"cloudflare"`
	StorageProvider api.StorageProvider `json:"storage_provider,omitempty"`
	BuildProvider   api.BuildProvider   `json:"build_provider,omitempty"`
	Config          string              `json:"config"`
}

// configResponse wraps RepoConfig with optional env reveal.
type configResponse struct {
	api.RepoConfig
	Env string `json:"env,omitempty"`
}

