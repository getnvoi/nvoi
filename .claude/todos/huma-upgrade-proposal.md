# Huma Upgrade Proposal

Migrate the nvoi API from swaggo/swag comment annotations to huma v2 type-driven OpenAPI generation.

## Why

### What we delete

- **8,153 lines of generated files** — `internal/api/docs/docs.go`, `swagger.json`, `swagger.yaml`. Gone. Never committed again.
- **446 lines of swagger comment annotations** — every `// @Summary`, `// @Param`, `// @Success`, `// @Failure`, `// @Router` comment block across 12 handler files.
- **`bin/swag`** — the build step that installs swaggo CLI and regenerates docs. Gone.
- **3 swaggo dependencies** — `swaggo/swag`, `swaggo/gin-swagger`, `swaggo/files` (plus their transitive deps: `go-openapi/spec`, `KyleBanks/depth`, etc.).
- **Comment/code drift risk** — annotations are stringly-typed. Rename a field, forget to update the comment, spec is wrong silently. With huma, the types ARE the spec.

### What we gain

- **OpenAPI 3.1** — generated at runtime from Go types. Always correct. No build step.
- **Compile-time contract** — change an input struct field, every handler using it gets a compiler error. Today, change `swagger_types.go`, the comments don't know.
- **Request validation for free** — huma validates incoming JSON against the struct tags (`required`, `maxLength`, `minimum`, `enum`, etc.) before the handler runs. Today we rely on Gin's `binding:"required"` which is weaker.
- **Consistent error format** — huma returns RFC 7807 Problem Details on validation failures. Today we hand-roll `gin.H{"error": ...}` in 50+ places.
- **Typed path/query params** — `workspace_id` parsed as UUID with validation in the input struct. Today we read `c.Param("workspace_id")` as raw string, pass it to GORM, hope for the best.
- **No `gin.Context` leak** — handlers receive `context.Context` + typed input, return typed output + error. Pure functions. Testable without httptest.

### What stays the same

- **Gin** remains the router — `humagin.New(ginEngine, config)` adapter. No framework swap.
- **GORM** — unchanged. Models, queries, transactions all stay.
- **Auth middleware** — `api.AuthRequired(db)` stays as Gin middleware. Huma routes go through the same Gin middleware stack.
- **`pkg/core/`** — untouched. Handlers still call the same functions.
- **`internal/api/models.go`** — untouched. Same DB models.
- **Encryption, JWT, GitHub verification** — untouched.
- **Executor, dbOutput** — untouched. Deployment execution is internal, not exposed via handler signatures.

## Scope

### Files to change

| File | Action | Effort |
|---|---|---|
| `cmd/api/main.go` | Add huma adapter init | Small |
| `handlers/router.go` | Replace Gin route registration with `huma.Register()` calls, remove swaggo imports | Medium |
| `handlers/auth.go` | Rewrite `LoginHandler` to huma signature | Small |
| `handlers/workspaces.go` | Rewrite 5 handlers + extract `loadWorkspace` to middleware | Medium |
| `handlers/repos.go` | Rewrite 5 handlers + extract `loadRepo` to middleware | Medium |
| `handlers/config.go` | Rewrite 4 handlers (PushConfig is the most complex) | Medium |
| `handlers/deploy.go` | Rewrite 5 handlers, DeploymentLogs needs custom streaming | Medium |
| `handlers/describe.go` | Rewrite 2 handlers, remove helper funcs (move to shared) | Small |
| `handlers/ssh.go` | Rewrite 1 handler, streaming response | Small |
| `handlers/query.go` | Rewrite 11 handlers, 4 streaming | Medium |
| `handlers/swagger_types.go` | **Delete entirely** — types move into input/output structs per handler | Delete |
| `handlers/credentials.go` | Keep as-is — internal helper, not a handler | None |
| `handlers/output.go` | Keep as-is — internal, not a handler | None |
| `handlers/executor.go` | Keep as-is — internal | None |
| `internal/api/docs/` | **Delete entire directory** — no more generated files | Delete |
| `bin/swag` | **Delete** | Delete |
| `go.mod` | Remove swaggo deps, add huma v2 | Small |
| `CLAUDE.md` | Update swagger section | Small |
| `internal/api/CLAUDE.md` | Update architecture, remove swagger docs section | Small |

### Files unchanged

- `internal/api/models.go` — DB models stay
- `internal/api/auth.go` — JWT middleware stays (Gin middleware)
- `internal/api/jwt.go` — stays
- `internal/api/github.go` — stays
- `internal/api/db.go` — stays
- `internal/api/encrypt.go` — stays
- `internal/api/config/` — stays (YAML parsing, validation)
- `internal/api/plan/` — stays
- `internal/api/managed/` — stays
- `handlers/executor.go` — stays
- `handlers/output.go` — stays
- `handlers/credentials.go` — stays

## Handler migration pattern

### Before (Gin + swaggo)

```go
// @Summary     Create workspace
// @Description Creates a new workspace and assigns the authenticated user as owner.
// @Tags        workspaces
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       body body     createWorkspaceRequest true "Workspace name"
// @Success     201  {object} api.Workspace
// @Failure     400  {object} errorResponse
// @Failure     401  {object} errorResponse
// @Failure     500  {object} errorResponse
// @Router      /workspaces [post]
func CreateWorkspace(db *gorm.DB) gin.HandlerFunc {
    return func(c *gin.Context) {
        user := api.CurrentUser(c)
        var req createWorkspaceRequest
        if err := c.ShouldBindJSON(&req); err != nil {
            c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
            return
        }
        // ... business logic ...
        c.JSON(http.StatusCreated, workspace)
    }
}
```

### After (huma)

```go
type CreateWorkspaceInput struct {
    Body struct {
        Name string `json:"name" required:"true" minLength:"1" doc:"Workspace name"`
    }
}

type CreateWorkspaceOutput struct {
    Body api.Workspace
}

func CreateWorkspace(db *gorm.DB) func(context.Context, *CreateWorkspaceInput) (*CreateWorkspaceOutput, error) {
    return func(ctx context.Context, input *CreateWorkspaceInput) (*CreateWorkspaceOutput, error) {
        user := api.UserFromContext(ctx)
        // ... same business logic, no c.JSON, no c.ShouldBindJSON ...
        return &CreateWorkspaceOutput{Body: workspace}, nil
    }
}
```

### Registration (before)

```go
ws.POST("", CreateWorkspace(db))
```

### Registration (after)

```go
huma.Register(api, huma.Operation{
    OperationID: "create-workspace",
    Method:      http.MethodPost,
    Path:        "/workspaces",
    Summary:     "Create workspace",
    Description: "Creates a new workspace and assigns the authenticated user as owner.",
    Tags:        []string{"workspaces"},
    Security:    []map[string][]string{{"BearerAuth": {}}},
}, CreateWorkspace(db))
```

## Key design decisions

### 1. loadWorkspace / loadRepo — pure functions, not context writers

Today `loadWorkspace` and `loadRepo` write directly to `gin.Context` on failure (404 response). In huma, handlers return errors — they don't write responses.

Keep as plain functions that return `(*model, error)`. Handler calls them, returns error on failure:

```go
func findWorkspace(db *gorm.DB, userID, workspaceID string) (*api.Workspace, error) {
    var ws api.Workspace
    result := db.Joins("JOIN workspace_users ...").Where("...").First(&ws)
    if result.Error != nil {
        return nil, huma.Error404NotFound("workspace not found")
    }
    return &ws, nil
}
```

No more writing to `gin.Context`. Pure function. Returns error.

### 2. Auth — stay as Gin middleware

`api.AuthRequired(db)` is Gin middleware that sets the user on `gin.Context`. With humagin, Gin middleware runs first, then huma handles the request. The user is already in the context when the huma handler runs.

Add a helper to extract the user:

```go
func UserFromContext(ctx context.Context) *api.User {
    // humagin propagates gin.Context values
}
```

Verify in Phase 1 that humagin properly propagates Gin context values to huma's `context.Context`. If not, need a huma middleware adapter.

### 3. Streaming endpoints — 4 handlers need special treatment

`DeploymentLogs`, `ServiceLogs`, `ExecCommand`, `RunSSH` all stream responses.

Use `huma.StreamResponse` for NDJSON (DeploymentLogs — structured format worth documenting). Keep text/plain streams (ServiceLogs, ExecCommand, RunSSH) as raw Gin handlers — OpenAPI can't describe opaque byte streams meaningfully.

```go
func DeploymentLogs(db *gorm.DB) func(context.Context, *DeploymentLogsInput) (*huma.StreamResponse, error) {
    return func(ctx context.Context, input *DeploymentLogsInput) (*huma.StreamResponse, error) {
        return &huma.StreamResponse{
            Body: func(ctx huma.Context) {
                ctx.SetHeader("Content-Type", "application/x-ndjson")
                // write JSONL lines to ctx.BodyWriter()
            },
        }, nil
    }
}
```

### 4. Path params — shared base struct

Every repo-scoped endpoint has `workspace_id` and `repo_id`. One shared struct, embedded everywhere:

```go
type RepoScopedInput struct {
    WorkspaceID string `path:"workspace_id" format:"uuid" doc:"Workspace ID"`
    RepoID      string `path:"repo_id" format:"uuid" doc:"Repo ID"`
}

type GetConfigInput struct {
    RepoScopedInput
    Reveal bool `query:"reveal" default:"false" doc:"Show env values"`
}
```

Eliminates all `c.Param("workspace_id")` calls. Adds UUID format validation for free.

### 5. Error responses — backward compatibility

Today: `gin.H{"error": err.Error()}`. Huma defaults to RFC 7807 Problem Details.

**Decision:** configure huma's error transformer to emit `{"error": "..."}` format to stay backward-compatible with the cloud CLI. Migrate to RFC 7807 later as a separate change.

## Migration order

5 phases, each independently shippable and testable.

### Phase 1: Setup + health (1 endpoint)

- Add `huma/v2` to `go.mod`
- Create huma adapter in `cmd/api/main.go`
- Migrate `/health` to huma
- Verify auth middleware bridging
- Both swaggo and huma specs served simultaneously

### Phase 2: Auth + workspaces (6 endpoints)

- `POST /login`
- 5 workspace CRUD handlers
- Rewrite `loadWorkspace` to pure function
- Add `UserFromContext` helper

### Phase 3: Repos + config (9 endpoints)

- 5 repo CRUD handlers
- Rewrite `loadRepo` to pure function
- 4 config handlers (PushConfig is the complex one)

### Phase 4: Deploy + cluster (7 endpoints)

- Deploy, ListDeployments, GetDeployment, RunDeployment
- DeploymentLogs (streaming)
- DescribeCluster, ListResources

### Phase 5: Query + cleanup (14 endpoints)

- 11 query handlers
- SSH, ServiceLogs, ExecCommand (streaming)
- Delete `internal/api/docs/`, `bin/swag`, `swagger_types.go`
- Remove swaggo deps from `go.mod`
- Update CLAUDE.md files

## File-by-file changes

### `cmd/api/main.go`

Add huma adapter alongside Gin engine. Replace `handlers.NewRouter` with `handlers.RegisterRoutes` that takes both `huma.API` and `*gin.Engine`.

### `handlers/router.go` — rewrite

Replace `NewRouter` with `RegisterRoutes(api huma.API, r *gin.Engine, db *gorm.DB, verify api.GitHubVerifier)`. All `huma.Register()` calls. Remove swaggo imports and docs import. Huma serves its own docs at `/docs`.

### `handlers/swagger_types.go` — DELETE

Types dissolve into per-handler input/output structs:

| swagger_types.go type | Becomes |
|---|---|
| `createWorkspaceRequest` | `CreateWorkspaceInput.Body` |
| `updateWorkspaceRequest` | `UpdateWorkspaceInput.Body` |
| `createRepoRequest` | `CreateRepoInput.Body` |
| `updateRepoRequest` | `UpdateRepoInput.Body` |
| `pushConfigRequest` | `PushConfigInput.Body` |
| `runSSHRequest` | `RunSSHInput.Body` |
| `execRequest` | `ExecInput.Body` |
| `buildPruneRequest` | `PruneInput.Body` |
| `errorResponse` | Huma built-in (RFC 7807 or custom transformer) |
| `validationErrorResponse` | Huma built-in validation errors |
| `deleteResponse` | `DeleteWorkspaceOutput.Body` |
| `statusResponse` | `RunDeploymentOutput.Body` |
| `healthResponse` | `HealthOutput.Body` |
| `planResponse` | `PlanConfigOutput.Body` |
| `configListItem` | `ListConfigsOutput.Body` element |
| `configResponse` | `GetConfigOutput.Body` |
| `buildLatestResponse` | `BuildLatestOutput.Body` |

### `handlers/auth.go` — rewrite handler

Replace `gin.HandlerFunc` with `func(context.Context, *LoginInput) (*LoginOutput, error)`. Remove 12 lines of annotations. `loginRequest`/`loginResponse` become `LoginInput.Body`/`LoginOutput.Body`.

### `handlers/workspaces.go` — rewrite 5 handlers

Each handler: remove annotations, change signature, replace `c.JSON`/`c.ShouldBindJSON`/`c.Param` with typed input/output. `loadWorkspace` becomes `findWorkspace` returning error.

### `handlers/repos.go` — rewrite 5 handlers

Same pattern. `loadRepo` becomes `findRepo` returning error.

### `handlers/config.go` — rewrite 4 handlers

PushConfig is the most complex handler. Business logic unchanged — only the request/response plumbing changes. Enum validation (`compute_provider`) becomes automatic via struct tags.

### `handlers/deploy.go` — rewrite 5 handlers

DeploymentLogs uses `huma.StreamResponse`. Others are straightforward JSON in/out.

### `handlers/describe.go` — rewrite 2 handlers

Helpers (`clusterFromLatestConfig`, `latestConfigAndEnv`) stay as internal functions.

### `handlers/ssh.go` — rewrite or keep as Gin

Streaming text/plain. Either `huma.StreamResponse` or stay as raw Gin handler.

### `handlers/query.go` — rewrite 11 handlers

8 JSON handlers (trivial). 3 streaming (ServiceLogs, ExecCommand — keep as Gin or StreamResponse).

### `internal/api/docs/` — DELETE (3 files, 8,153 lines)

### `bin/swag` — DELETE (10 lines)

### Test files — rewrite alongside handlers

Same assertions, different setup. ~2,000 lines across 9 test files. Mechanical.

## Effort estimate

| Phase | Endpoints | Hours |
|---|---|---|
| Phase 1: Setup + health | 1 | 1 |
| Phase 2: Auth + workspaces | 6 | 3 |
| Phase 3: Repos + config | 9 | 4 |
| Phase 4: Deploy + cluster | 7 | 3 |
| Phase 5: Query + cleanup | 14 | 4 |
| **Total** | **37** | **~15** |

## Risks

1. **humagin context bridging** — Gin middleware sets user on `gin.Context`. Huma handlers get `context.Context`. Must verify the bridge works. Test in Phase 1.
2. **Streaming responses** — `huma.StreamResponse` is less documented than standard handlers. May need raw Gin fallback for 4 endpoints.
3. **Error format** — RFC 7807 vs `{"error": "..."}`. Cloud CLI must be updated or huma's error transformer configured for backward compatibility.
4. **Test volume** — ~2,000 lines of test rewrites. Mechanical but not free.
5. **huma version stability** — v2.37+ is stable but verify no breaking changes during migration window.
