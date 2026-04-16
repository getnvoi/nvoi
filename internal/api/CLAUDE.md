# CLAUDE.md — internal/api

Control plane. Stores config, teams, audit data, and receives events from agents. Does NOT execute infrastructure operations — the agent on the master node does all execution.

## How it works

```
Agent (on master) deploys infrastructure
  → teeOutput sends events to Reporter
  → Reporter POSTs batched JSONL to API
  → API stores as AgentEvent rows
  → Dashboard reads AgentEvent for audit/monitoring

CLI commands:
  → SSH tunnel to agent → agent executes → JSONL back to CLI
  → API is not in the CLI→agent path (read-only dashboard)
```

Config CRUD (future — currently via nvoi.yaml):
```
CLI: GET /repos/:rid/config → parse YAML → mutate → PUT /repos/:rid/config
  → API: validate YAML parses → inject app+env from repo → ValidateConfig (warn) → save
```

## Architecture

```
internal/api/
  models.go              User, Workspace, WorkspaceUser, InfraProvider, Repo, CommandLog, AgentEvent
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
    auth.go              POST /login
    workspaces.go        CRUD /workspaces
    repos.go             CRUD /repos — scoped through workspace + provider FK links
    agent_events.go      POST /agent/events — batched event ingestion from agents
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

## Data model

```
User
  └── WorkspaceUser (join, role)
        └── Workspace
              ├── InfraProvider (kind + name + encrypted credentials)
              └── Repo
                    ├── Config (YAML blob)
                    ├── AgentToken + AgentTokenHash (auto-generated, for agent auth)
                    ├── SSHPrivateKey + SSHPublicKey (auto-generated)
                    ├── Provider FKs (Compute, DNS, Storage, Build, Secrets)
                    ├── CommandLog (per-command summary)
                    └── AgentEvent (per-event detail, FK with cascade)
```

## Agent event ingestion

`POST /agent/events` — accepts batched JSONL events from agents. Own auth (agent token, not JWT). The agent sends its plaintext token (from `NVOI_API_TOKEN`), the API hashes it with SHA-256 and compares against `Repo.AgentTokenHash`.

Events are stored as `AgentEvent` rows with `RepoID` FK (indexed, cascades on delete). App/env denormalized for fast queries.

## Key rules

1. **The API is a control plane — it never executes infrastructure operations.** Agents execute. The API stores and serves data.
2. **Provider credentials come from InfraProvider records.** Encrypted at rest via AES-256-GCM.
3. **Repo SSH keys and agent tokens are auto-generated.** Ed25519 keypair + 32-byte agent token created at repo creation.
4. **The API NEVER reads env vars for business logic.** `os.Getenv` is only for server startup config (`MAIN_DATABASE_URL`, `JWT_SECRET`, `ENCRYPTION_KEY`).
5. **Agent auth is separate from user auth.** Agents use hashed bearer tokens. Users use JWT. The `/agent/events` endpoint bypasses the JWT middleware.
6. **Never fail silently.** Encryption errors, token generation errors, DB errors — always return the error.
