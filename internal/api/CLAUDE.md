# CLAUDE.md — internal/api

Thin relay between the cloud CLI and `pkg/core/`. Each command hits one API endpoint that calls one `pkg/core/` function. No config YAML, no planner, no deployment lifecycle.

## How it works

```
CLI command (e.g. nvoi instance set master ...)
  → CloudBackend.run("instance.set", "master", {type, region, role})
    → POST /repos/:rid/run {kind, name, params}
      → Load repo + InfraProvider credentials
      → dispatch() → pkg/core.ComputeSet()
      → Stream JSONL output back
      → Log CommandLog row
    → CLI renders JSONL through TUI
```

## Architecture

```
internal/api/
  models.go              User, Workspace, WorkspaceUser, InfraProvider, Repo, CommandLog
  db.go                  PostgreSQL + GORM + AutoMigrate
  testdb.go              In-memory SQLite for tests
  encrypt.go             AES-256-GCM for secrets at rest
  jwt.go                 HS256 JWT, 30-day TTL
  auth.go                AuthRequired middleware, CurrentUser
  github.go              GitHub token verification (PAT, OAuth, fine-grained)

  handlers/
    router.go            Huma route registration + Gin auth middleware
    humaerr.go           Custom error format ({"error":"..."})
    inputs.go            Shared input types (WorkspaceScopedInput, RepoScopedInput)
    run.go               POST /run — single dispatch endpoint, streams JSONL, logs CommandLog
    auth.go              POST /login
    workspaces.go        CRUD /workspaces
    repos.go             CRUD /repos — scoped through workspace + provider FK links
    providers.go         Set/list/delete InfraProvider records (workspace-scoped)
    describe.go          GET /describe + GET /resources + clusterFromRepo helper
    query.go             Read-only endpoints (instances, volumes, dns, secrets, storage, builds, logs, exec)
    ssh.go               POST /ssh — run command on master, stream output
```

## Data model

```
User
  └── WorkspaceUser (join, role)
        └── Workspace
              ├── InfraProvider (kind + name + encrypted credentials)
              └── Repo
                    ├── ComputeProviderID → InfraProvider
                    ├── DNSProviderID → InfraProvider
                    ├── StorageProviderID → InfraProvider
                    ├── BuildProviderID → InfraProvider
                    └── CommandLog (one row per /run call)
```

## The /run endpoint

Single endpoint for all mutation commands. Takes `{kind, name, params}`, dispatches to `pkg/core/`, streams JSONL, logs the result.

Supported kinds: `instance.set`, `instance.delete`, `volume.set`, `volume.delete`, `firewall.set`, `build`, `secret.set`, `secret.delete`, `storage.set`, `storage.delete`, `storage.empty`, `service.set`, `service.delete`, `cron.set`, `cron.delete`, `dns.set`, `dns.delete`, `ingress.set`, `ingress.delete`.

The `runner` struct holds per-request state: `Cluster` + provider refs built from `Repo.InfraProvider` links. Constructed once per request. `dispatch()` is the switch statement mapping kind → `pkg/core/` function.

## Provider credentials

Credentials live on `InfraProvider` records at workspace scope. Repos link to providers via FK columns. No env-based credential resolution on the API side — `InfraProvider.CredentialsMap()` returns schema-mapped credentials directly.

```
nvoi provider set compute hetzner    → InfraProvider row (encrypted)
nvoi repos use myapp --compute hetzner → Repo.ComputeProviderID FK
nvoi instance set master ...         → POST /run → repo.ComputeProvider.CredentialsMap()
```

## Authentication

1. CLI resolves GitHub token: `gh auth token` → `GITHUB_TOKEN` env → interactive prompt
2. `POST /login {"github_token": "..."}` → API calls `api.github.com/user` → verifies identity
3. Find/create User + default Workspace → issue JWT (30-day TTL)
4. CLI stores JWT in `~/.config/nvoi/auth.json`
5. All subsequent requests: `Authorization: Bearer <jwt>`

## API routes

```
POST   /login                                          public — GitHub token → JWT
GET    /health                                         public — status check

GET    /workspaces                                     list user's workspaces
POST   /workspaces                                     create workspace
GET    /workspaces/:id                                 get workspace
PUT    /workspaces/:id                                 update workspace
DELETE /workspaces/:id                                 delete workspace

GET    /workspaces/:wid/providers                      list providers
POST   /workspaces/:wid/providers                      set provider
DELETE /workspaces/:wid/providers/:kind/:name           delete provider

GET    /workspaces/:wid/repos                          list repos
POST   /workspaces/:wid/repos                          create repo
GET    /workspaces/:wid/repos/:rid                     get repo
PUT    /workspaces/:wid/repos/:rid                     update repo (link providers)
DELETE /workspaces/:wid/repos/:rid                     delete repo

POST   /workspaces/:wid/repos/:rid/run                 execute command (streams JSONL)

GET    /workspaces/:wid/repos/:rid/describe             live cluster state
GET    /workspaces/:wid/repos/:rid/resources            provider resources
POST   /workspaces/:wid/repos/:rid/ssh                  run command on master

GET    /workspaces/:wid/repos/:rid/instances            list servers
GET    /workspaces/:wid/repos/:rid/volumes              list volumes
GET    /workspaces/:wid/repos/:rid/dns                  list DNS records
GET    /workspaces/:wid/repos/:rid/secrets              list secret keys
GET    /workspaces/:wid/repos/:rid/storage              list storage buckets
POST   /workspaces/:wid/repos/:rid/storage/:name/empty  empty bucket

GET    /workspaces/:wid/repos/:rid/builds               list registry images
GET    /workspaces/:wid/repos/:rid/builds/:name/latest   latest image ref
POST   /workspaces/:wid/repos/:rid/builds/:name/prune    prune old tags

GET    /workspaces/:wid/repos/:rid/services/:svc/logs   stream pod logs
POST   /workspaces/:wid/repos/:rid/services/:svc/exec    run command in pod
```

## Key rules

1. **The API calls `pkg/core/` — it never reimplements infrastructure logic.** Same functions the direct CLI uses.
2. **Provider credentials come from InfraProvider records.** No env-based resolution. `CredentialsMap()` returns schema-mapped keys.
3. **Repo SSH keys are auto-generated.** Ed25519 keypair created at repo creation, private key encrypted at rest.
4. **The API NEVER reads env vars for business logic.** `os.Getenv` is only for server startup config (`DATABASE_URL`, `JWT_SECRET`, `ENCRYPTION_KEY`).
5. **CommandLog is the history.** One row per /run call. Kind, name, status, duration. No JSONL replay — the CLI renders in real-time.
6. **Never fail silently.** Encryption errors, SSH key generation errors, DB errors — always return the error.
