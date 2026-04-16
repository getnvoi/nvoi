package handlers

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humagin"
	"github.com/getnvoi/nvoi/internal/api"
	sentrygin "github.com/getsentry/sentry-go/gin"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// NewRouter creates the API router. The API is a control plane — it stores
// config, teams, and audit data. It does NOT execute operations. The agent
// on the master node does all execution and reports events back.
func NewRouter(db *gorm.DB, verify api.GitHubVerifier) *gin.Engine {
	r := gin.Default()
	r.Use(sentrygin.New(sentrygin.Options{Repanic: true}))

	r.Use(func(c *gin.Context) {
		switch c.Request.URL.Path {
		case "/health", "/openapi.json", "/openapi.yaml", "/openapi-3.0.json", "/openapi-3.0.yaml", "/docs":
			c.Next()
			return
		}
		if c.Request.URL.Path == "/login" && c.Request.Method == "POST" {
			c.Next()
			return
		}
		// Agent events endpoint has its own auth (workspace token, not JWT).
		if c.Request.URL.Path == "/agent/events" && c.Request.Method == "POST" {
			c.Next()
			return
		}
		api.AuthRequired(db)(c)
	})

	config := huma.DefaultConfig("nvoi API", "1.0.0")
	config.Info.Description = "Control plane for nvoi. Stores config, teams, and audit data. Agents execute operations."
	config.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"BearerAuth": {
			Type:         "http",
			Scheme:       "bearer",
			BearerFormat: "JWT",
			Description:  "JWT token from POST /login",
		},
	}

	humaAPI := humagin.New(r, config)
	security := []map[string][]string{{"BearerAuth": {}}}

	// ── Public ───────────────────────────────────────────────────────────────

	huma.Register(humaAPI, huma.Operation{
		OperationID: "health", Method: http.MethodGet, Path: "/health",
		Summary: "Health check", Tags: []string{"system"},
	}, func(_ context.Context, _ *struct{}) (*struct {
		Body struct {
			Status string `json:"status"`
		}
	}, error) {
		return &struct {
			Body struct {
				Status string `json:"status"`
			}
		}{Body: struct {
			Status string `json:"status"`
		}{Status: "ok"}}, nil
	})

	huma.Register(humaAPI, huma.Operation{
		OperationID: "login", Method: http.MethodPost, Path: "/login",
		Summary: "Login with GitHub token", Tags: []string{"auth"},
	}, LoginHandler(db, verify))

	// ── Workspaces ───────────────────────────────────────────────────────────

	huma.Register(humaAPI, huma.Operation{
		OperationID: "list-workspaces", Method: http.MethodGet, Path: "/workspaces",
		Summary: "List workspaces", Tags: []string{"workspaces"}, Security: security,
	}, ListWorkspaces(db))

	huma.Register(humaAPI, huma.Operation{
		OperationID: "create-workspace", Method: http.MethodPost, Path: "/workspaces",
		Summary: "Create workspace", Tags: []string{"workspaces"}, Security: security,
		DefaultStatus: http.StatusCreated,
	}, CreateWorkspace(db))

	huma.Register(humaAPI, huma.Operation{
		OperationID: "get-workspace", Method: http.MethodGet, Path: "/workspaces/{workspace_id}",
		Summary: "Get workspace", Tags: []string{"workspaces"}, Security: security,
	}, GetWorkspace(db))

	huma.Register(humaAPI, huma.Operation{
		OperationID: "update-workspace", Method: http.MethodPut, Path: "/workspaces/{workspace_id}",
		Summary: "Update workspace", Tags: []string{"workspaces"}, Security: security,
	}, UpdateWorkspace(db))

	huma.Register(humaAPI, huma.Operation{
		OperationID: "delete-workspace", Method: http.MethodDelete, Path: "/workspaces/{workspace_id}",
		Summary: "Delete workspace", Tags: []string{"workspaces"}, Security: security,
	}, DeleteWorkspace(db))

	// ── Repos ────────────────────────────────────────────────────────────────
	rp := "/workspaces/{workspace_id}/repos"
	rpID := rp + "/{repo_id}"

	huma.Register(humaAPI, huma.Operation{
		OperationID: "list-repos", Method: http.MethodGet, Path: rp,
		Summary: "List repos", Tags: []string{"repos"}, Security: security,
	}, ListRepos(db))

	huma.Register(humaAPI, huma.Operation{
		OperationID: "create-repo", Method: http.MethodPost, Path: rp,
		Summary: "Create repo", Tags: []string{"repos"}, Security: security,
		DefaultStatus: http.StatusCreated,
	}, CreateRepo(db))

	huma.Register(humaAPI, huma.Operation{
		OperationID: "get-repo", Method: http.MethodGet, Path: rpID,
		Summary: "Get repo", Tags: []string{"repos"}, Security: security,
	}, GetRepo(db))

	huma.Register(humaAPI, huma.Operation{
		OperationID: "update-repo", Method: http.MethodPut, Path: rpID,
		Summary: "Update repo", Tags: []string{"repos"}, Security: security,
	}, UpdateRepo(db))

	huma.Register(humaAPI, huma.Operation{
		OperationID: "delete-repo", Method: http.MethodDelete, Path: rpID,
		Summary: "Delete repo", Tags: []string{"repos"}, Security: security,
	}, DeleteRepo(db))

	// ── Config ──────────────────────────────────────────────────────────────
	cfgPath := rpID + "/config"

	huma.Register(humaAPI, huma.Operation{
		OperationID: "config-show", Method: http.MethodGet, Path: cfgPath,
		Summary: "Show stored config", Tags: []string{"config"}, Security: security,
	}, ConfigShow(db))

	huma.Register(humaAPI, huma.Operation{
		OperationID: "config-save", Method: http.MethodPut, Path: cfgPath,
		Summary: "Save config", Tags: []string{"config"}, Security: security,
	}, ConfigSave(db))

	// ── Agent events ────────────────────────────────────────────────────────
	// Ingests JSONL events from agents. Own auth (workspace token), not JWT.
	huma.Register(humaAPI, huma.Operation{
		OperationID: "agent-events", Method: http.MethodPost, Path: "/agent/events",
		Summary: "Ingest agent events", Tags: []string{"agent"},
	}, AgentEvents(db))

	return r
}
