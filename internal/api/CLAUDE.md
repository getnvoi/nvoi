# CLAUDE.md — internal/api

Thin relay between the cloud CLI and `pkg/core/`. Each command hits one API endpoint that calls one `pkg/core/` function.

## How it works

```
CLI command (e.g. nvoi deploy)
  → POST /repos/:rid/deploy {config: yamlString}
    → Load repo + InfraProvider credentials
    → Reconcile engine runs against the cluster
    → Stream JSONL output back
  → CLI renders JSONL through TUI
```

For operational commands:
```
CLI command (e.g. nvoi cron run db-backup)
  → POST /repos/:rid/cron/db-backup/run
    → pkg/core.CronRun()
    → Stream JSONL output back
```

For config CRUD:
```
CLI command (e.g. nvoi config service set web --build web --port 3000)
  → CLI: GET /repos/:rid/config → parse YAML → mutate → PUT /repos/:rid/config
    → API: validate YAML parses → inject app+env from repo → ValidateConfig (warn) → save
    → Returns: updated YAML + validation warnings
```

## Architecture

```
internal/api/
  models.go              User, Workspace, WorkspaceUser, InfraProvider, Repo (with Config field), CommandLog
  db.go                  PostgreSQL + GORM + AutoMigrate (reads MAIN_DATABASE_URL)
  testdb.go              In-memory SQLite for tests
  encrypt.go             AES-256-GCM for secrets at rest
  jwt.go                 HS256 JWT, 30-day TTL
  auth.go                AuthRequired middleware, CurrentUser
  github.go              GitHub token verification (PAT, OAuth, fine-grained)

  handlers/
    router.go            Huma route registration + Gin auth middleware
    humaerr.go           Custom error format ({"error":"..."})
    inputs.go            Shared input types (WorkspaceScopedInput, RepoScopedInput)
    config.go            GET /config (show) + PUT /config (save with validation warnings)
    cron.go              POST /cron/{name}/run — trigger cron job, stream JSONL
    stream.go            Shared JSONL streaming output (jsonlOutput, streamOperation)
    auth.go              POST /login
    workspaces.go        CRUD /workspaces
    repos.go             CRUD /repos — scoped through workspace + provider FK links
    providers.go         Set/list/delete InfraProvider records (workspace-scoped)
    describe.go          GET /describe + GET /resources + clusterFromRepo helper
    query.go             Read-only endpoints (instances, volumes, dns, secrets, storage, builds, logs, exec)
    ssh.go               POST /ssh — run command on master, stream output
```

## Deployment

The API is deployed via `nvoi.yaml` as a service within the nvoi cluster itself:

```yaml
database:
  main:
    image: postgres:17
    volume: pgdata

services:
  api:
    build: api
    port: 8080
    secrets: [JWT_SECRET, ENCRYPTION_KEY]

domains:
  api: [api.nvoi.to]
```

The database package auto-injects `MAIN_DATABASE_URL`, `MAIN_POSTGRES_HOST`, etc. into the API service. The API reads `MAIN_DATABASE_URL` from its environment to connect to postgres.

Database credentials are user-owned — set in `.env` and GitHub secrets as `MAIN_POSTGRES_USER`, `MAIN_POSTGRES_PASSWORD`, `MAIN_POSTGRES_DB`. No auto-generation.

Backups are managed by the database package — CronJob runs every 6 hours, uploads to R2 bucket. `nvoi db backup now` triggers immediately.

## Data model

```
User
  └── WorkspaceUser (join, role)
        └── Workspace
              ├── InfraProvider (kind + name + encrypted credentials)
              └── Repo
                    ├── Config (YAML blob — mutated by config CRUD, used by deploy)
                    ├── ComputeProviderID → InfraProvider
                    ├── DNSProviderID → InfraProvider
                    ├── StorageProviderID → InfraProvider
                    ├── BuildProviderID → InfraProvider
                    └── CommandLog
```

## Config CRUD

Config is stored as a YAML blob on the Repo. CLI commands fetch, mutate, save:

- `GET /config` — returns stored YAML + validation warnings
- `PUT /config` — receives YAML, validates it parses, injects app+env from repo, runs `ValidateConfig` (warnings not rejections), saves

The API is dumb storage. Surgical YAML manipulation happens in the CLI (`internal/cloud/config_*.go`). Save always succeeds (unless malformed YAML). Validation warnings are informational — deploy is the hard gate.

## Key rules

1. **The API calls `pkg/core/` — it never reimplements infrastructure logic.**
2. **Provider credentials come from InfraProvider records.** No env-based resolution. `CredentialsMap()` returns schema-mapped keys.
3. **Repo SSH keys are auto-generated.** Ed25519 keypair created at repo creation, private key encrypted at rest.
4. **The API NEVER reads env vars for business logic.** `os.Getenv` is only for server startup config (`MAIN_DATABASE_URL`, `JWT_SECRET`, `ENCRYPTION_KEY`).
5. **Config save always succeeds.** Validation warnings returned, not rejections. Deploy rejects invalid config.
6. **Never fail silently.** Encryption errors, SSH key generation errors, DB errors — always return the error.
