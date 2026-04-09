package handlers

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humagin"
	"github.com/getnvoi/nvoi/internal/api"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// NewRouter creates the API router with huma OpenAPI + Gin.
func NewRouter(db *gorm.DB, verify api.GitHubVerifier) *gin.Engine {
	r := gin.Default()

	// Auth middleware on a group — Gin runs it before huma handles the request.
	r.Use(func(c *gin.Context) {
		// Public paths skip auth.
		switch c.Request.URL.Path {
		case "/health", "/openapi.json", "/openapi.yaml", "/openapi-3.0.json", "/openapi-3.0.yaml", "/docs":
			c.Next()
			return
		}
		if c.Request.URL.Path == "/login" && c.Request.Method == "POST" {
			c.Next()
			return
		}
		// Everything else requires auth.
		api.AuthRequired(db)(c)
	})

	config := huma.DefaultConfig("nvoi API", "1.0.0")
	config.Info.Description = "Deploy containers to cloud servers. Push a config YAML + env, get an ordered deployment plan, execute it."
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
		OperationID: "health",
		Method:      http.MethodGet,
		Path:        "/health",
		Summary:     "Health check",
		Tags:        []string{"system"},
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
		OperationID: "login",
		Method:      http.MethodPost,
		Path:        "/login",
		Summary:     "Login with GitHub token",
		Description: "Verifies a GitHub token and returns a JWT. Creates user and default workspace on first login.",
		Tags:        []string{"auth"},
	}, LoginHandler(db, verify))

	// ── Workspaces ───────────────────────────────────────────────────────────

	huma.Register(humaAPI, huma.Operation{
		OperationID: "list-workspaces",
		Method:      http.MethodGet,
		Path:        "/workspaces",
		Summary:     "List workspaces",
		Tags:        []string{"workspaces"},
		Security:    security,
	}, ListWorkspaces(db))

	huma.Register(humaAPI, huma.Operation{
		OperationID:   "create-workspace",
		Method:        http.MethodPost,
		Path:          "/workspaces",
		Summary:       "Create workspace",
		Tags:          []string{"workspaces"},
		Security:      security,
		DefaultStatus: http.StatusCreated,
	}, CreateWorkspace(db))

	huma.Register(humaAPI, huma.Operation{
		OperationID: "get-workspace",
		Method:      http.MethodGet,
		Path:        "/workspaces/{workspace_id}",
		Summary:     "Get workspace",
		Tags:        []string{"workspaces"},
		Security:    security,
	}, GetWorkspace(db))

	huma.Register(humaAPI, huma.Operation{
		OperationID: "update-workspace",
		Method:      http.MethodPut,
		Path:        "/workspaces/{workspace_id}",
		Summary:     "Update workspace",
		Tags:        []string{"workspaces"},
		Security:    security,
	}, UpdateWorkspace(db))

	huma.Register(humaAPI, huma.Operation{
		OperationID: "delete-workspace",
		Method:      http.MethodDelete,
		Path:        "/workspaces/{workspace_id}",
		Summary:     "Delete workspace",
		Tags:        []string{"workspaces"},
		Security:    security,
	}, DeleteWorkspace(db))

	// ── Providers ───────────────────────────────────────────────────────────
	prov := "/workspaces/{workspace_id}/providers"

	huma.Register(humaAPI, huma.Operation{
		OperationID: "list-providers", Method: http.MethodGet, Path: prov,
		Summary: "List providers", Tags: []string{"providers"}, Security: security,
	}, ListProviders(db))

	huma.Register(humaAPI, huma.Operation{
		OperationID: "set-provider", Method: http.MethodPost, Path: prov,
		Summary: "Set provider", Tags: []string{"providers"}, Security: security,
	}, SetProvider(db))

	huma.Register(humaAPI, huma.Operation{
		OperationID: "delete-provider", Method: http.MethodDelete, Path: prov + "/{kind}/{name}",
		Summary: "Delete provider", Tags: []string{"providers"}, Security: security,
	}, DeleteProvider(db))

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

	// ── Config ───────────────────────────────────────────────────────────────
	cfg := rpID + "/config"

	huma.Register(humaAPI, huma.Operation{
		OperationID: "push-config", Method: http.MethodPost, Path: cfg,
		Summary: "Push config", Tags: []string{"config"}, Security: security,
		DefaultStatus: http.StatusCreated,
	}, PushConfig(db))

	huma.Register(humaAPI, huma.Operation{
		OperationID: "get-config", Method: http.MethodGet, Path: cfg,
		Summary: "Get latest config", Tags: []string{"config"}, Security: security,
	}, GetConfig(db))

	huma.Register(humaAPI, huma.Operation{
		OperationID: "list-configs", Method: http.MethodGet, Path: rpID + "/configs",
		Summary: "List config versions", Tags: []string{"config"}, Security: security,
	}, ListConfigs(db))

	huma.Register(humaAPI, huma.Operation{
		OperationID: "plan-config", Method: http.MethodGet, Path: cfg + "/plan",
		Summary: "Get execution plan", Tags: []string{"config"}, Security: security,
	}, PlanConfig(db))

	// ── Deploy ───────────────────────────────────────────────────────────────
	dep := rpID + "/deployments"
	depID := dep + "/{deployment_id}"

	huma.Register(humaAPI, huma.Operation{
		OperationID: "deploy", Method: http.MethodPost, Path: rpID + "/deploy",
		Summary: "Create deployment", Tags: []string{"deployments"}, Security: security,
		DefaultStatus: http.StatusCreated,
	}, Deploy(db))

	huma.Register(humaAPI, huma.Operation{
		OperationID: "list-deployments", Method: http.MethodGet, Path: dep,
		Summary: "List deployments", Tags: []string{"deployments"}, Security: security,
	}, ListDeployments(db))

	huma.Register(humaAPI, huma.Operation{
		OperationID: "get-deployment", Method: http.MethodGet, Path: depID,
		Summary: "Get deployment", Tags: []string{"deployments"}, Security: security,
	}, GetDeployment(db))

	huma.Register(humaAPI, huma.Operation{
		OperationID: "run-deployment", Method: http.MethodPost, Path: depID + "/run",
		Summary: "Run deployment", Tags: []string{"deployments"}, Security: security,
	}, RunDeployment(db))

	huma.Register(humaAPI, huma.Operation{
		OperationID: "deployment-logs", Method: http.MethodGet, Path: depID + "/logs",
		Summary: "Stream deployment logs", Tags: []string{"deployments"}, Security: security,
	}, DeploymentLogs(db))

	// ── Cluster ──────────────────────────────────────────────────────────────

	huma.Register(humaAPI, huma.Operation{
		OperationID: "describe-cluster", Method: http.MethodGet, Path: rpID + "/describe",
		Summary: "Describe cluster", Tags: []string{"cluster"}, Security: security,
	}, DescribeCluster(db))

	huma.Register(humaAPI, huma.Operation{
		OperationID: "list-resources", Method: http.MethodGet, Path: rpID + "/resources",
		Summary: "List provider resources", Tags: []string{"cluster"}, Security: security,
	}, ListResources(db))

	huma.Register(humaAPI, huma.Operation{
		OperationID: "run-ssh", Method: http.MethodPost, Path: rpID + "/ssh",
		Summary: "Run SSH command", Tags: []string{"cluster"}, Security: security,
	}, RunSSH(db))

	// ── Infrastructure queries ───────────────────────────────────────────────

	huma.Register(humaAPI, huma.Operation{
		OperationID: "list-instances", Method: http.MethodGet, Path: rpID + "/instances",
		Summary: "List instances", Tags: []string{"instances"}, Security: security,
	}, ListInstances(db))

	huma.Register(humaAPI, huma.Operation{
		OperationID: "list-volumes", Method: http.MethodGet, Path: rpID + "/volumes",
		Summary: "List volumes", Tags: []string{"volumes"}, Security: security,
	}, ListVolumes(db))

	huma.Register(humaAPI, huma.Operation{
		OperationID: "list-dns", Method: http.MethodGet, Path: rpID + "/dns",
		Summary: "List DNS records", Tags: []string{"dns"}, Security: security,
	}, ListDNSRecords(db))

	huma.Register(humaAPI, huma.Operation{
		OperationID: "list-secrets", Method: http.MethodGet, Path: rpID + "/secrets",
		Summary: "List secrets", Tags: []string{"secrets"}, Security: security,
	}, ListSecrets(db))

	huma.Register(humaAPI, huma.Operation{
		OperationID: "list-storage", Method: http.MethodGet, Path: rpID + "/storage",
		Summary: "List storage", Tags: []string{"storage"}, Security: security,
	}, ListStorageBuckets(db))

	huma.Register(humaAPI, huma.Operation{
		OperationID: "empty-storage", Method: http.MethodPost, Path: rpID + "/storage/{name}/empty",
		Summary: "Empty storage bucket", Tags: []string{"storage"}, Security: security,
	}, EmptyStorage(db))

	// ── Builds ───────────────────────────────────────────────────────────────
	bld := rpID + "/builds"

	huma.Register(humaAPI, huma.Operation{
		OperationID: "list-builds", Method: http.MethodGet, Path: bld,
		Summary: "List builds", Tags: []string{"builds"}, Security: security,
	}, ListBuilds(db))

	huma.Register(humaAPI, huma.Operation{
		OperationID: "build-latest", Method: http.MethodGet, Path: bld + "/{name}/latest",
		Summary: "Get latest build image", Tags: []string{"builds"}, Security: security,
	}, BuildLatestImage(db))

	huma.Register(humaAPI, huma.Operation{
		OperationID: "build-prune", Method: http.MethodPost, Path: bld + "/{name}/prune",
		Summary: "Prune build images", Tags: []string{"builds"}, Security: security,
	}, PruneBuild(db))

	// ── Services ─────────────────────────────────────────────────────────────
	svc := rpID + "/services/{service}"

	huma.Register(humaAPI, huma.Operation{
		OperationID: "service-logs", Method: http.MethodGet, Path: svc + "/logs",
		Summary: "Stream service logs", Tags: []string{"services"}, Security: security,
	}, ServiceLogs(db))

	huma.Register(humaAPI, huma.Operation{
		OperationID: "service-exec", Method: http.MethodPost, Path: svc + "/exec",
		Summary: "Exec in service pod", Tags: []string{"services"}, Security: security,
	}, ExecCommand(db))

	return r
}
