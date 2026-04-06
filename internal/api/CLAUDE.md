# CLAUDE.md — internal/api

The SaaS layer on top of `pkg/core/`. Users push a config YAML + env, the API validates it, plans the deploy sequence, and executes `pkg/core/` functions in order. Same result as running `examples/core/{provider}/deploy` scripts by hand.

## How it works

```
User pushes config YAML + .env
  → Parse YAML
  → Expand managed services (postgres, redis, meilisearch → real service specs)
  → Validate expanded config
  → Plan: config → ordered []Step sequence
  → Diff: previous config vs current → delete steps for removed resources
  → FullPlan: deletes first (reverse order) + sets (forward order, idempotent)
  → Executor walks steps, calls pkg/core/ functions
  → Each step tracked in deployment_steps, each output line in deployment_step_logs (JSONL)
```

## Architecture

```
internal/api/
  models.go              All models + provider enums + deployment lifecycle
  db.go                  PostgreSQL + GORM + AutoMigrate
  testdb.go              In-memory SQLite for tests
  encrypt.go             AES-256-GCM for secrets at rest
  jwt.go                 HS256 JWT, 30-day TTL
  auth.go                AuthRequired middleware, CurrentUser
  github.go              GitHub token verification (PAT, OAuth, fine-grained)

  handlers/
    router.go            Gin routes — /health, /login, /workspaces, /repos, /config, /deploy
    auth.go              POST /login — verify GitHub token → find/create user → issue JWT
    workspaces.go        CRUD /workspaces — scoped via workspace_users join
    repos.go             CRUD /workspaces/:id/repos — scoped through workspace
    config.go            Config push/get/list/plan — the core deploy pipeline entry point
    deploy.go            POST deploy, POST run, GET deployments, GET deployment, GET logs (JSONL)
    executor.go          Execute() — walks deployment steps, calls pkg/core/ functions, writes JSONL logs
    output.go            dbOutput — implements pkg/core.Output, writes JSONL to deployment_step_logs

  config/
    schema.go            Public config YAML struct — servers, volumes, build, storage, services, domains
    validate.go          15 validation rules with cross-reference checks
    plan.go              Config + env → ordered Step sequence (mirrors examples/core/{provider}/deploy)

  managed/
    service.go           ManagedService interface + registry + Expand()
    postgres.go          PostgreSQL — image, port, volume, credentials, DATABASE_ prefix
    redis.go             Redis — image, port, REDIS_ prefix
    meilisearch.go       Meilisearch — image, port, volume, master key, MEILI_ prefix
```

## Data model

```
User
  └── WorkspaceUser (join, role)
        └── Workspace
              └── Repo
                    ├── RepoConfig (versioned)
                    │     ├── compute_provider   enum: hetzner | aws | scaleway
                    │     ├── dns_provider       enum: cloudflare | aws (optional)
                    │     ├── storage_provider   enum: cloudflare | aws (optional)
                    │     ├── build_provider     enum: local | daytona | github (optional)
                    │     ├── config             YAML text
                    │     └── env                encrypted KEY=VALUE (credentials + app secrets)
                    │
                    ├── RepoManagedServiceConfig (permanent, not versioned)
                    │     ├── name               "db", "cache", "search"
                    │     ├── kind               "postgres", "redis", "meilisearch"
                    │     └── credentials        encrypted JSON (generated once, stored forever)
                    │
                    └── Deployment
                          ├── status             pending → running → succeeded | failed
                          └── DeploymentStep
                                ├── position, kind, name, params (JSON)
                                ├── status       pending → running → succeeded | failed | skipped
                                └── DeploymentStepLog
                                      └── line   JSONL (same format as --json CLI output)
```

## Config schema (what users write)

```yaml
servers:
  master:
    type: cx23
    region: fsn1
  worker-1:
    type: cx33
    region: fsn1

volumes:
  pgdata:
    size: 30
    server: master

build:
  web:
    source: benbonnet/dummy-rails

storage:
  assets:
    cors: true

services:
  db:
    managed: postgres          # managed service — auto image, port, volume, credentials
  web:
    build: web                 # references build target
    port: 80
    replicas: 2
    health: /up
    server: worker-1
    env:
      - RAILS_ENV=production   # literal
      - POSTGRES_USER           # resolved from .env
    secrets:
      - RAILS_MASTER_KEY       # resolved from .env, stored as k8s secret
    storage:
      - assets                 # expands to STORAGE_ASSETS_* secret refs
    uses:
      - db                     # injects DATABASE_DB_* credential secrets
  jobs:
    build: web
    command: bin/jobs
    uses: [db]

domains:
  web: final.nvoi.to           # single string or list: [a.com, b.com]
```

### Service source (mutually exclusive)

| Field | Meaning |
|-------|---------|
| `image: postgres:17` | Pre-built image |
| `build: web` | References a build target — image resolved at deploy time |
| `managed: postgres` | Managed service — Expand() replaces with real spec + generates credentials |

### Env resolution

- `RAILS_ENV=production` → literal, passed through
- `POSTGRES_USER` (no `=`) → looked up from .env, becomes `POSTGRES_USER=<value>`
- Missing key → hard error at plan time

### Secret aliasing

- `POSTGRES_PASSWORD` → k8s secret key = env var name (same)
- `POSTGRES_PASSWORD=POSTGRES_PASSWORD_DB` → container reads `POSTGRES_PASSWORD`, backed by namespaced secret key `POSTGRES_PASSWORD_DB`

Used by managed services to avoid collisions when multiple instances of the same kind exist.

## Managed services

Interface: `Kind()`, `Spec(name)`, `Credentials(name)`, `EnvPrefix()`, `InternalSecrets(name, creds)`.

One file per implementation. Registration via `init()`. Adding a new managed service = one new file, five methods.

`Spec()` returns what the managed service itself consumes (image, port, volumes, its own env/secrets). `InternalSecrets()` returns what other services consume when they `uses:` it (namespaced credential keys).

### Credential lifecycle

1. First config push with `managed: postgres` for `db` → `Credentials("db")` generates random password → stored in `repo_managed_service_configs` (encrypted)
2. Every subsequent push → `Expand()` loads stored credentials → same password forever
3. Row deleted → credentials gone, service stops being injected

### Secret namespacing

Multiple instances of the same kind get unique secret keys:
- `db` (postgres) → `POSTGRES_PASSWORD_DB`
- `analytics` (postgres) → `POSTGRES_PASSWORD_ANALYTICS`

Spec uses aliased format: `POSTGRES_PASSWORD=POSTGRES_PASSWORD_DB` so the container reads the standard env var while the k8s secret key is namespaced.

### Expand() transformation

```
Config (public)                    Config (internal)
services:                          services:
  db:                                db:
    managed: postgres        →         image: postgres:17
                                       port: 5432
                                       volumes: [db-data:/var/lib/postgresql/data]
                                       secrets: [POSTGRES_PASSWORD=POSTGRES_PASSWORD_DB]
  web:                               web:
    uses: [db]               →         secrets: [DATABASE_DB_HOST, DATABASE_DB_PORT, ...]
```

Expand also auto-adds volumes required by managed services to the config.

## Plan (config → steps)

Seven phases, same order as `examples/core/{provider}/deploy`:

| Phase | StepKind | Maps to |
|-------|----------|---------|
| 1. Compute | `instance.set` | `pkg/core.ComputeSet` |
| 2. Volumes | `volume.set` | `pkg/core.VolumeSet` |
| 3. Build | `build` | `pkg/core.BuildRun` |
| 4. Secrets | `secret.set` | `pkg/core.SecretSet` |
| 5. Storage | `storage.set` | `pkg/core.StorageSet` |
| 6. Services | `service.set` | `pkg/core.ServiceSet` |
| 7. DNS | `dns.set` | `pkg/core.DNSSet` |

All steps are deterministic (sorted keys). First server alphabetically = master.

## Diff (removals)

`Diff(prev, current)` generates delete steps for resources that disappeared. Reverse order of deploy:

| Phase | StepKind | Maps to |
|-------|----------|---------|
| 1. DNS | `dns.delete` | `pkg/core.DNSDelete` |
| 2. Services | `service.delete` | `pkg/core.ServiceDelete` |
| 3. Storage | `storage.delete` | `pkg/core.StorageDelete` |
| 4. Secrets | `secret.delete` | `pkg/core.SecretDelete` |
| 5. Volumes | `volume.delete` | `pkg/core.VolumeDelete` |
| 6. Compute | `instance.delete` | `pkg/core.ComputeDelete` |

`FullPlan(prev, current, env)` = delete steps first + set steps. Delete commands are idempotent — deleting something that doesn't exist succeeds silently.

## Authentication

1. CLI resolves GitHub token: `gh auth token` → `GITHUB_TOKEN` env → interactive prompt
2. `POST /login {"github_token": "..."}` → API calls `api.github.com/user` → verifies identity
3. Find/create User + default Workspace → issue JWT (30-day TTL)
4. CLI stores JWT in `~/.config/nvoi/auth.json`
5. All subsequent requests: `Authorization: Bearer <jwt>`

Token-type agnostic: PAT, OAuth access token, fine-grained token all work.

## Encryption

- `ENCRYPTION_KEY` env var — 32 bytes hex-encoded (AES-256-GCM)
- `RepoConfig.Env` encrypted at rest (GORM `BeforeCreate` / `AfterFind` hooks)
- `RepoManagedServiceConfig.Credentials` encrypted at rest (same hooks)
- Env hidden from JSON by default — `?reveal=true` to show

## Compose

```
core      direct mode CLI (cmd/core via bin/entrypoint)
api       REST server (cmd/api via bin/api-entrypoint), depends_on postgres (healthy)
cli       cloud CLI (cmd/cli via bin/cli-entrypoint), depends_on api (healthy)
postgres  PostgreSQL 17
```

**Compose handles the full dependency chain.** `bin/cloud login` starts postgres → waits healthy → starts api → waits healthy → runs cli. One command, no manual startup. Never run `docker compose up -d postgres` separately — `depends_on` with `condition: service_healthy` handles it.

See [`examples/README.md`](../../examples/README.md) for deploy workflows.

## Deploy flow

```
CLI: nvoi deploy
  → POST .../deploy → creates Deployment + Steps (all pending)
  → POST .../deployments/:id/run → starts executor in goroutine
  → Poll GET .../deployments/:id (status) + GET .../deployments/:id/logs (JSONL)
  → Render logs through TUI — same output as examples/core/hetzner/deploy

API: POST .../deploy
  → Load latest RepoConfig + previous version
  → Expand managed services (load stored creds from repo_managed_service_configs)
  → FullPlan(prev, current, env) = Diff deletes + Plan sets
  → Create Deployment + DeploymentSteps rows (all pending)
  → Return deployment with steps

API: POST .../deployments/:id/run
  → Load Deployment + RepoConfig + Repo
  → Build executor: map raw env vars to provider schema keys via provider.MapCredentials()
  → Start Execute() in goroutine

Executor (walks steps in order):
  → For each step: status → running → call pkg/core/ function
  → pkg/core/ emits Output events → dbOutput writes JSONL to deployment_step_logs
  → Each stdout/stderr line = one row in deployment_step_logs
  → Step done: status → succeeded | failed
  → On failure: remaining steps → skipped
  → Deployment: succeeded | failed
```

### Credential mapping

The executor receives raw `.env` content (parsed from DB). Provider schemas expect internal keys (`token`, not `HETZNER_TOKEN`). `provider.MapCredentials(schema, env)` translates env var names → schema keys. This is the same function the direct CLI uses — single source of truth in `pkg/provider/resolve.go`.

## Rendering

Renderers live in `internal/render/` — shared by both direct CLI and cloud CLI.

- `internal/render/tui.go` — lipgloss styled (terminal)
- `internal/render/plain.go` — CI/non-TTY
- `internal/render/json.go` — JSONL
- `internal/render/table.go` — bordered tables (describe, resources)
- `internal/render/resolve.go` — pick renderer (--json, --ci, TTY detect)
- `internal/render/replay.go` — JSONL line → Output call (bridge between API logs and renderers)

The API's `dbOutput` writes JSONL to DB. The CLI reads JSONL from the API and replays it through the same TUI renderer that `pkg/core/` uses directly. Identical output regardless of direct or cloud mode.

## API routes

```
POST   /login                                          public — GitHub token → JWT
GET    /health                                         public — status check

GET    /workspaces                                     list user's workspaces
POST   /workspaces                                     create workspace
GET    /workspaces/:id                                 get workspace
PUT    /workspaces/:id                                 update workspace
DELETE /workspaces/:id                                 delete workspace

GET    /workspaces/:wid/repos                          list repos
POST   /workspaces/:wid/repos                          create repo
GET    /workspaces/:wid/repos/:rid                     get repo
PUT    /workspaces/:wid/repos/:rid                     update repo
DELETE /workspaces/:wid/repos/:rid                     delete repo

POST   /workspaces/:wid/repos/:rid/config              push config (versioned)
GET    /workspaces/:wid/repos/:rid/config               get latest config
GET    /workspaces/:wid/repos/:rid/configs              list config versions
GET    /workspaces/:wid/repos/:rid/config/plan          execution plan for latest

POST   /workspaces/:wid/repos/:rid/deploy               trigger deployment
GET    /workspaces/:wid/repos/:rid/deployments           list deployments
GET    /workspaces/:wid/repos/:rid/deployments/:did      get deployment + steps + logs
POST   /workspaces/:wid/repos/:rid/deployments/:did/run  start executing a pending deployment
GET    /workspaces/:wid/repos/:rid/deployments/:did/logs raw JSONL log stream

GET    /workspaces/:wid/repos/:rid/describe          live cluster state
GET    /workspaces/:wid/repos/:rid/resources          provider resources
```

## Cloud CLI commands (internal/cli/)

```
nvoi login                   authenticate (gh CLI → GITHUB_TOKEN → prompt)
nvoi whoami                  show user/workspace/repo context
nvoi workspaces list         list workspaces (* = active)
nvoi workspaces create       create workspace
nvoi workspaces use          set active workspace
nvoi workspaces delete       delete workspace
nvoi repos list              list repos in active workspace (* = active)
nvoi repos create            create repo
nvoi repos use               set active repo
nvoi repos delete            delete repo
nvoi push                    push config YAML + .env to active repo
nvoi plan                    show execution plan for latest config
nvoi deploy                  trigger deploy, stream logs through TUI
nvoi logs <id>               stream deployment logs through TUI
nvoi describe                live cluster state (nodes, workloads, pods, services)
nvoi resources               list all provider resources
```

See [`examples/README.md`](../../examples/README.md) for full deploy workflows (direct + cloud mode).
See [`examples/cloud/`](../../examples/cloud/) for config YAML examples.
See [`examples/core/`](../../examples/core/) for imperative command sequences.

## Key rules

1. **The API calls `pkg/core/` — it never reimplements infrastructure logic.** Same functions the direct CLI uses. Same idempotency guarantees.
2. **Config describes what. Env provides where + credentials.** Provider selection is on `RepoConfig` columns. Provider credentials are in the encrypted env.
3. **Managed service credentials are permanent.** Generated once, stored forever in `repo_managed_service_configs`. Not versioned. Row exists = inject.
4. **Expand happens before validation.** Public config (with `managed:`) → internal config (with real specs) → validate the expanded result.
5. **Plan is deterministic.** Same config + env always produces the same step sequence. Sorted keys everywhere.
6. **Diff only handles removals.** Set commands are idempotent — no need to diff for changes. Only need to detect what disappeared between versions.
7. **Secrets are always secrets.** Managed service passwords use namespaced k8s secret keys with aliased env vars. Never plain text in specs.
8. **Renderers are shared.** `internal/render/` is the single source for TUI/Plain/JSON output. Both CLIs and the API use the same event types. No duplicate formatting code.
9. **JSONL is the transport.** API stores raw JSONL in DB. API returns raw JSONL to CLI. CLI renders through shared renderers. API never formats for display.
10. **One line = one record.** Each Output event = one JSONL line = one `deployment_step_logs` row. No aggregation, no batching.
11. **The API NEVER reads env vars for business logic.** `os.Getenv` is only for server startup config (`DATABASE_URL`, `JWT_SECRET`, `ENCRYPTION_KEY`). Everything else comes from the DB: app name from `Repo.Name`, environment from `Repo.Environment`, provider selection from `RepoConfig` typed columns, SSH keys from `Repo.SSHPrivateKey` (auto-generated, encrypted), credentials from `RepoConfig.Env` (encrypted). Never `env["NVOI_APP_NAME"]`, never `env["COMPUTE_PROVIDER"]`, never `env["SSH_KEY"]`.
12. **Repo SSH keys are auto-generated.** Ed25519 keypair created at repo creation, private key encrypted at rest. The API uses `Repo.SSHPrivateKey` to SSH into servers — no file paths, no user input.
13. **Never fail silently.** Encryption errors, SSH key generation errors, DB errors — always return the error. Never swallow, never log-and-continue.
