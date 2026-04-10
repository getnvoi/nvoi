# CLAUDE.md — nvoi

## What nvoi is

A CLI that deploys containers to cloud servers from a declarative YAML config. `nvoi deploy` reconciles live infrastructure to match the config. `nvoi teardown` nukes it all. `nvoi describe` fetches everything live from the cluster.

## Philosophy

- **`app` + `env` is the namespace.** Defined in `nvoi.yaml`. `nvoi-{app}-{env}-*`. Different app or env = brand new infrastructure.
- **No state files.** No manifest, no database, no local cache. Infrastructure is the source of truth.
- **Everything is idempotent.** `nvoi deploy` reconciles: adds desired resources, removes orphans. Run twice, same result.
- **Naming is the lookup key.** `nvoi-{app}-{env}-{resource}`. Deterministic. No UUIDs. The naming convention finds everything.
- **Reconcile vs teardown.** Reconcile converges on a diff: queries live state, adds what's missing, removes what's orphaned — manages everything (provider infra and k8s resources). Teardown is a hard nuke: no diff, no live state query, wipes all external provider resources. K8s resources die with the servers. Volumes and storage preserved by default — `--delete-volumes` / `--delete-storage` to nuke.
- **Declarative config, imperative reconciliation.** `nvoi.yaml` declares desired state. The reconciler walks each resource type in order.
- **Packages.** Higher-level abstractions that bundle infra + secrets + CLI. `database:` is the first package — creates StatefulSet, headless Service, credentials, backup bucket, backup CronJob from one config block. Packages hook into the reconcile loop between secrets and storage.
- **Provider interfaces scale.** Hetzner, Cloudflare, AWS, Scaleway. Interface-first. Add a provider = implement the interface. Organized by domain: `compute/`, `dns/`, `storage/`, `build/`.
- **SSH is the transport.** No agent binary. Single SSH connection per deploy (`MasterSSH`), reused across all operations.
- **Secrets are k8s secrets.** Values live in the cluster only. Resolved from environment variables at deploy time.

## Build & Test

```bash
go test ./...
go test ./... -v
go test ./... -cover
go build ./cmd/core
```

## CI

GitHub Actions workflows (`.github/workflows/`):

- **ci.yml** — fmt + vet + test + build on push and PR
- **deploy.yml** — production deploy on push to main (runs `bin/deploy`)
- **release.yml** — cross-compile on git tags (`v*`), upload to R2

**PR merges:** Never squash. Use `gh pr merge --merge --delete-branch`.

## Local development

`bin/nvoi` is the universal entrypoint. Sources `.env`, builds the binary if needed, runs any command.

```bash
# Deploy / teardown
bin/nvoi deploy                # reconcile from nvoi.yaml
bin/nvoi teardown              # nuke provider resources
bin/deploy                     # shorthand for bin/nvoi deploy
bin/destroy                    # shorthand for bin/nvoi teardown

# Operate
bin/nvoi describe              # live cluster state
bin/nvoi logs web              # stream logs
bin/nvoi logs api -f           # follow logs
bin/nvoi exec web -- sh        # shell into service pod
bin/nvoi ssh -- kubectl get pods  # run command on master

# Database
bin/nvoi db sql "SELECT 1;"   # run SQL
bin/nvoi db backup now         # trigger backup
bin/nvoi db backup list        # list backups in R2
bin/nvoi db backup download <name> -f ./backup.sql.gz

# Cron
bin/nvoi cron run db-backup    # trigger cron job

# Inspect
bin/nvoi resources             # list all provider resources
go test ./...                  # run tests
```

### Files

| File | Purpose |
|------|---------|
| `nvoi.yaml` | Infrastructure config (tracked) |
| `.env` | Provider credentials + app secrets (not tracked) |
| `bin/nvoi` | Universal entrypoint — sources .env, builds, runs |
| `bin/deploy` | Shorthand for `bin/nvoi deploy` |
| `bin/destroy` | Shorthand for `bin/nvoi teardown` |
| `bin/core` | Direct `go run ./cmd/core` (no .env) |
| `bin/cloud` | Cloud CLI — starts compose (API + postgres), runs `cmd/cli` |
| `bin/dev` | Website development loop |

### Cloud CLI (local development)

`bin/cloud` auto-starts the API + postgres via docker-compose, then runs the cloud CLI:

```bash
bin/cloud login                # authenticate with GitHub token
bin/cloud whoami               # show current user + context
bin/cloud workspaces list      # list workspaces
bin/cloud repos create myapp   # create repo
bin/cloud repos use myapp      # set active repo
bin/cloud provider set compute hetzner  # link provider
bin/cloud deploy               # deploy via API (sends YAML, streams JSONL)
bin/cloud describe             # live cluster state via API
bin/cloud logs web             # stream logs via API
```

The API runs at `localhost:8080` with a local postgres. `docker-compose.yml` handles the stack:
- **api** — Go binary, reads `MAIN_DATABASE_URL`, serves Huma REST API
- **postgres** — postgres:17-alpine, dev credentials (nvoi/nvoi/nvoi)

The cloud CLI stores auth in `~/.config/nvoi/auth.json`.

## Config format

```yaml
app: myapp
env: production

providers:
  compute: hetzner          # hetzner | aws | scaleway
  dns: cloudflare           # cloudflare | aws | scaleway
  storage: cloudflare       # cloudflare | aws | scaleway
  build: local              # local | daytona | github

servers:
  master:
    type: cax11
    region: nbg1
    role: master

firewall: default            # string or list of port:cidr rules

volumes:
  pgdata:
    size: 20
    server: master

database:                    # package — bundles StatefulSet, Service, credentials, backup
  main:
    image: postgres:17       # required — also supports mysql, mariadb
    volume: pgdata           # required — references defined volume

secrets:                     # user secrets, resolved from env vars
  - JWT_SECRET
  - ENCRYPTION_KEY

storage:
  releases: {}

build:
  web: ./cmd/web

services:
  api:
    build: api
    port: 8080
    secrets: [JWT_SECRET, ENCRYPTION_KEY]
  web:
    build: web
    port: 3000
    server: master           # single node — nodeSelector
    # servers: [worker-1, worker-2]  # multi-node — nodeAffinity + topologySpread

crons:
  cleanup:
    image: busybox
    schedule: "0 1 * * *"
    command: echo hi

domains:
  web: [myapp.com, www.myapp.com]
  api: [api.myapp.com]
```

### Validation

`ValidateConfig()` + `packages.ValidateAll()` run before touching infrastructure:

- `app` and `env` required
- `providers.compute` required
- At least one server, exactly one master, all have type/region/role
- Volumes: size > 0, server exists
- Services/crons: image XOR build, referenced build/storage/volumes exist
- Volume mounts: `name:/path` format, volume must be on same server as workload
- `server` and `servers` mutually exclusive. Multiple servers + volume = error.
- Web-facing services (with domains): replicas omitted → defaults to 2. Explicit `replicas: 1` → hard error.
- Database: image and volume required, storage provider required, name collisions checked.

## Commands

```bash
nvoi deploy                              # reconcile to match config
nvoi teardown                            # nuke external provider resources
nvoi teardown --delete-volumes --delete-storage
nvoi describe                            # live cluster state
nvoi resources                           # list all provider resources
nvoi logs <service>                      # stream service logs
nvoi logs <service> -f                   # follow logs
nvoi exec <service> -- cmd               # run command in service pod
nvoi ssh -- cmd                          # run command on master node
nvoi cron run <name>                     # trigger cron job immediately
nvoi db backup now                       # trigger database backup
nvoi db backup list                      # list backups in bucket
nvoi db backup download <name> [-f file] # download backup
nvoi db sql "SELECT ..."                 # run SQL on database pod
```

Global flags: `--config` (default: `nvoi.yaml`), `--json` (JSONL output), `--ci` (plain text).

## Architecture

```
cmd/
  core/main.go             Direct CLI entrypoint
  cli/main.go              Cloud CLI entrypoint
  api/main.go              API server entrypoint
  web/main.go              Marketing website (Gin + Goldmark)
  distribution/main.go     Binary distribution server (R2-backed)

internal/
  config/                  Shared types — no logic
    config.go              AppConfig, DeployContext, LiveState, all definition types
  reconcile/               Deploy orchestrator — YAML to infrastructure
    reconcile.go           Deploy() — ordered reconciliation with packages phase
    validate.go            ValidateConfig() — fail-fast pre-flight checks
    helpers.go             DescribeLive(), SplitServers(), ResolveServers()
    servers.go             ServersAdd (create) + ServersRemoveOrphans (drain + delete after services move)
    firewall.go            Firewall reconciliation
    volumes.go             Volume reconciliation
    build.go               Build reconciliation
    secrets.go             Secret reconciliation
    storage.go             Storage reconciliation (excludes package-managed buckets)
    services.go            Service reconciliation (excludes package-managed services, defaults replicas for domains)
    crons.go               Cron reconciliation (excludes package-managed crons)
    dns.go                 DNS reconciliation
    ingress.go             Ingress reconciliation
  packages/                Package interface and registry
    package.go             Package interface, ValidateAll, ReconcileAll, TeardownAll
    database/              Database package — postgres + mysql engine support
      database.go          Reconcile, Validate, Teardown, env var injection
      engine.go            Engine interface + Postgres/MySQL implementations
      credentials.go       Read from env vars (no auto-generation)
      manifests.go         StatefulSet + headless Service YAML generation
      backup.go            Backup CronJob generation
  core/                    Direct CLI commands + env resolution
    deploy.go              NewDeployCmd
    teardown.go            NewTeardownCmd
    describe.go            NewDescribeCmd, NewResourcesCmd
    logs.go                NewLogsCmd
    exec.go                NewExecCmd
    ssh.go                 NewSSHCmd
    cron.go                NewCronCmd (cron run)
    database.go            NewDatabaseCmd (db backup now/list/download, db sql)
    resolve.go             BuildContext() — viper + env vars → DeployContext
  cli/                     Cloud CLI — HTTP relay to API
    backend.go             deploy/teardown/describe/resources/logs/exec/ssh/cron
    client.go              APIClient
    auth.go                Auth config (~/.config/nvoi/auth.json)
    login.go               GitHub token → JWT flow
    provider.go            nvoi provider set/list/delete
    repos.go               nvoi repos create/list/use/delete
    workspaces.go          nvoi workspaces
    whoami.go              nvoi whoami
  api/                     REST API server (Huma + Gin + GORM)
    models.go              User, Workspace, WorkspaceUser, InfraProvider, Repo, CommandLog
    db.go                  PostgreSQL + AutoMigrate (reads MAIN_DATABASE_URL)
    handlers/              Route handlers
  render/                  Output renderers — TUI, Plain, JSON
  testutil/                MockSSH, MockCompute, MockDNS, MockBucket, MockOutput

pkg/
  core/                    Business logic. One file per domain. No cobra, no I/O, no stdout.
    cluster.go             Cluster struct (MasterSSH field), ProviderRef, Connect(), SSH()
    compute.go             ComputeSet (SSH connect, EnsureSwap, Docker, k3s, label), ComputeDelete, ComputeList
    service.go             ServiceSet, ServiceDelete
    dns.go                 DNSSet, DNSDelete, DNSList
    ingress.go             IngressSet (WaitForCertificate + WaitForHTTPS from server), IngressDelete
    storage.go             StorageSet, StorageDelete, StorageEmpty, StorageList
    secret.go              SecretSet, SecretDelete, SecretList, SecretReveal
    volume.go              VolumeSet, VolumeDelete, VolumeList
    build.go               BuildRun, BuildParallel, BuildList, BuildLatest, BuildPrune
    cron.go                CronSet, CronDelete, CronRun
    database.go            DatabaseBackupList, DatabaseBackupDownload, DatabaseSQL
    describe.go            Describe, DescribeJSON
    resources.go           Resources
    firewall.go            FirewallSet, FirewallList
    wait.go                WaitRollout
    exec.go                Exec
    ssh.go                 SSH
    logs.go                Logs
  kube/                    K8s YAML generation + kubectl over SSH + Caddy ingress + rollout
  infra/                   SSH, server bootstrap, k3s, Docker, swap, volume mounting
  provider/                Provider interfaces + per-domain implementations
    compute.go             ComputeProvider interface
    dns.go                 DNSProvider interface
    bucket.go              BucketProvider interface
    builder.go             BuildProvider interface
    resolve.go             Registration, credential schemas, resolution
    s3ops/                 Shared S3 operations (CORS, lifecycle, empty)
    compute/
      hetzner/             Hetzner Cloud (compute + volumes)
      aws/                 AWS (EC2 + VPC)
      scaleway/            Scaleway (compute)
    dns/
      cloudflare/          Cloudflare DNS
      aws/                 AWS Route53
      scaleway/            Scaleway DNS
    storage/
      cloudflare/          Cloudflare R2
      aws/                 AWS S3
      scaleway/            Scaleway Object Storage
    build/
      local/               Local docker buildx
      daytona/             Daytona remote sandbox
      github/              GitHub Actions
    hetznerbase/           Shared Hetzner HTTP client
    awsbase/               Shared AWS SDK config
    cfbase/                Shared Cloudflare HTTP client
    scwbase/               Shared Scaleway HTTP client
  utils/                   Pure utilities: naming, poll, httpclient, ssh keys, format, maps, params
    s3/                    S3-compatible operations with AWS Signature V4
```

### SSH model

One SSH connection per deploy. `Cluster.MasterSSH` is set once after `ServersAdd()`, shared across all subsequent operations via `borrowedSSH` (no-op Close). API dispatch path connects on-demand (no `MasterSSH`).

`ComputeSet` connects to individual servers via `Cluster.Connect()` for provisioning (Docker, k3s, swap). Those are separate connections — not the master.

SSH errors: `ErrHostKeyChanged` and `ErrAuthFailed` surface immediately with guidance. Stale known hosts auto-cleared on server creation.

### Reconcile flow

```
Deploy(ctx, dc, cfg, viper)
  → ValidateConfig(cfg)
  → packages.ValidateAll(cfg)
  → DescribeLive(ctx, dc) → LiveState
  → ServersAdd(ctx, dc, cfg)          — create desired, NO orphan removal yet
  → establish MasterSSH
  → Firewall(ctx, dc, live, cfg)
  → Volumes(ctx, dc, live, cfg)
  → Build(ctx, dc, cfg)
  → Secrets(ctx, dc, live, cfg, v)
  → packages.ReconcileAll(ctx, dc, cfg) → packageEnvVars
  → Storage(ctx, dc, live, cfg)
  → Services(ctx, dc, live, cfg, packageEnvVars)
  → Crons(ctx, dc, live, cfg, packageEnvVars)
  → ServersRemoveOrphans(ctx, dc, live, cfg) — drain + delete AFTER workloads moved
  → DNS(ctx, dc, live, cfg)
  → Ingress(ctx, dc, live, cfg)
```

### Database package

`database:` in config triggers the database package. Per database:
1. Detect engine from image (postgres, mysql, mariadb)
2. Read credentials from environment (required, no auto-generation)
3. Store as k8s Secret
4. Apply StatefulSet + headless Service
5. Wait for readiness probe
6. Create backup bucket
7. Apply backup CronJob
8. Return env vars for injection into all app services

Env vars injected (for database named `main` with postgres):
```
MAIN_DATABASE_URL, MAIN_POSTGRES_USER, MAIN_POSTGRES_PASSWORD,
MAIN_POSTGRES_DB, MAIN_POSTGRES_HOST, MAIN_POSTGRES_PORT
```

Package-managed resources (`main-db`, `main-db-backup`, `main-db-backups`) are protected from orphan detection in Services, Crons, and Storage reconcilers.

### Server provisioning

`ComputeSet` flow per server:
1. `EnsureServer` at provider (create or return existing)
2. Resolve private IP
3. Clear stale known host (recycled IPs)
4. Wait for SSH (poll `Connect`, hard error on host key changed / auth failed)
5. `EnsureSwap` — reads actual disk size via `df`, proportional swap (5%, 512MB–2GB)
6. `EnsureDocker`
7. Master: `InstallK3sMaster` + `EnsureRegistry`
8. Worker: `JoinK3sWorker` (reads token from master, installs agent)
9. `LabelNode`

Zero-downtime server replacement: `ServersAdd` creates new servers first, `Services`/`Crons` move workloads, `ServersRemoveOrphans` drains and deletes old servers after.

## Providers

Organized by domain with shared base clients:

| Kind | YAML key | Interface | Implementations |
|------|----------|-----------|----------------|
| Compute | `providers.compute` | `ComputeProvider` | hetzner, aws, scaleway |
| DNS | `providers.dns` | `DNSProvider` | cloudflare, aws, scaleway |
| Storage | `providers.storage` | `BucketProvider` | cloudflare (R2), aws (S3), scaleway |
| Build | `providers.build` | `BuildProvider` | local, daytona, github |

`ensureFirewall` only ensures the resource exists — never resets rules. Rules managed exclusively by `ReconcileFirewallRules` in the Firewall reconcile step.

## Key rules

1. `app` + `env` in `nvoi.yaml` are required. They're the namespace for everything.
2. No state files. Infrastructure is the truth.
3. `deploy` is idempotent. Run twice, same result.
4. `teardown` nukes external provider resources. Volumes and storage preserved by default.
5. Provider interfaces scale. Add a provider = implement the interface.
6. Naming: `nvoi-{app}-{env}-{resource}`. Deterministic. No UUIDs.
7. SSH keys injected via cloud-init only. Single SSH connection per deploy (`MasterSSH`).
8. **`os.Getenv` lives exclusively in `internal/core/`.** `pkg/core/`, `provider/`, `infra/`, `utils/` never read env vars.
9. **Providers are silent.** Never print or narrate. Output via `pkg/core/` → `Output` interface.
10. **`pkg/core/` never writes to stdout.** All output through `Output` interface.
11. **`pkg/core/` never imports `net/http`.** HTTP calls belong in `infra/` or `provider/`.
12. **Errors flow up, render once.** `pkg/core/` returns errors. Cobra renders via `Output.Error()`.
13. **No shell injection.** Secret values via file upload, not inline interpolation.
14. **Web-facing services require replicas >= 2.** Omitted defaults to 2, explicit 1 is a hard error.
15. **Package-managed resources are protected from orphan detection.**
16. **Database credentials are user-owned.** No auto-generation. Missing = hard error.

## Production hardening notes

- **`~` doesn't expand in Go.** `resolveSSHKey()` calls `expandHome()`.
- **`kubectl apply` does strategic merge, not full replace.** `kube.Apply()` uses `kubectl replace` first, falls back to `kubectl apply --server-side --force-conflicts`.
- **Caddy with `hostNetwork` uses `Recreate` strategy.** ConfigMap mounted as directory (not subPath) for auto-sync.
- **DNS and ingress are separate concerns.** DNS creates A records. Ingress owns Caddy.
- **HTTPS verification runs from the server** via SSH curl, not from the deploy client. No DNS propagation dependency. Cert check (`sudo test -f`) + health check (any non-5xx).
- **SSH host key changed = hard error** with guidance to clear known hosts. Auto-cleared on server creation.
- **Firewall never reset during server creation.** `ensureFirewall` only ensures existence.
- **Concurrency control on deploy workflows.** `concurrency: { group: deploy, cancel-in-progress: false }`.
