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
bin/test                   # enforced 5s timeout — MUST pass, no exceptions
bin/test -v                # verbose
go test ./... -cover       # coverage
go build ./cmd/cli
```

**Test suite MUST complete in under 2 seconds per package.** `bin/test` enforces this with `go test -timeout 2s`. Any test that exceeds this is broken — fix it by injecting mocks for I/O waits (SSH polls, HTTP retries, stability delays). Never sleep in tests. Override production timeouts with `kube.SetTestTiming(time.Millisecond, time.Millisecond)` in test `init()`. This is non-negotiable.

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
| `bin/nvoi` | Universal entrypoint — sources .env, builds `cmd/cli`, runs with `--local` |
| `bin/deploy` | Shorthand for `bin/nvoi deploy` |
| `bin/destroy` | Shorthand for `bin/nvoi teardown` |

### Cloud CLI (local development)

For cloud mode development, start the API + postgres via docker-compose, then run the CLI without `--local`:

```bash
docker compose up -d --wait     # start API + postgres
export NVOI_API_BASE="http://localhost:8080"
go run ./cmd/cli login          # authenticate with GitHub token
go run ./cmd/cli deploy         # deploy via API
```

The API runs at `localhost:8080` with a local postgres. `docker-compose.yml` handles the stack:
- **api** — Go binary, reads `MAIN_DATABASE_URL`, serves Huma REST API
- **postgres** — postgres:17-alpine, dev credentials (nvoi/nvoi/nvoi)

Auth stored in `~/.config/nvoi/auth.json`.

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
    disk: 50                   # root disk GB (optional, AWS + Scaleway only)

firewall: default            # string or list of port:cidr rules

volumes:
  pgdata:
    size: 20
    server: master

database:                    # package — bundles StatefulSet, Service, credentials, backup
  main:
    kind: postgres           # required — postgres or mysql
    image: postgres:17       # required — container image
    volume: pgdata           # required — references defined volume

secrets:                     # user secrets, resolved from env vars
  - JWT_SECRET
  - ENCRYPTION_KEY

storage:
  releases: {}

build:
  api: ./cmd/api

services:
  api:
    build: api
    port: 8080
    secrets: [JWT_SECRET, ENCRYPTION_KEY]
    server: master           # single node — nodeSelector
    # servers: [worker-1, worker-2]  # multi-node — nodeAffinity + topologySpread

crons:
  cleanup:
    image: busybox
    schedule: "0 1 * * *"
    command: echo hi

domains:
  api: [api.myapp.com]
```

### Validation

`ValidateConfig()` runs before touching infrastructure (includes package validation):

- `app` and `env` required
- `providers.compute` required
- At least one server, exactly one master, all have type/region/role. `disk` optional (creation-only, not resizable). Hetzner + `disk` = hard error (fixed per server type).
- Volumes: size > 0, server exists
- Services/crons: image XOR build, referenced build/storage/volumes exist
- Volume mounts: `name:/path` format, volume must be on same server as workload
- `server` and `servers` mutually exclusive. Multiple servers + volume = error.
- Web-facing services (with domains): replicas omitted → defaults to 2. Explicit `replicas: 1` → hard error. 2 replicas on a single `server:` node is valid — the rule ensures process-level redundancy, not node distribution.
- Database: kind required (postgres or mysql), image and volume required, storage provider required, name collisions checked.

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
  cli/                     CLI entrypoint — Backend interface, one file per command
    main.go                rootCmd, mode detection, --local flag
    backend.go             Backend interface (12 methods)
    local.go               localBackend — direct pkg/core calls
    cloud.go               cloudBackend — HTTP relay to API
    deploy.go..ssh.go      One file per command, backend-agnostic
  api/main.go              API server entrypoint
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
  core/                    Source-agnostic logic — teardown, database helpers
    teardown.go            Teardown() — ordered resource deletion
    database.go            DatabaseBackupList, DatabaseBackupDownload, DatabaseSQL
  cloud/                   Cloud API client + cloud-only commands
    client.go              APIClient (HTTP)
    auth.go                Auth config (~/.config/nvoi/auth.json)
    backend.go             StreamRun, Describe, Resources, Logs, Exec, SSH, database ops
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
  testutil/                MockSSH (utils.SSHClient), MockCompute, MockDNS, MockBucket, MockOutput

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
    compute/               See pkg/provider/compute/CLAUDE.md for DeleteServer contract
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

**Interface → implementation → mock:**
- `utils.SSHClient` (`pkg/utils/ssh.go`) — the interface. Every SSH consumer takes this.
- `infra.SSHClient` (`pkg/infra/ssh.go`) — the real implementation. Wraps `golang.org/x/crypto/ssh` with SFTP upload, TCP dial, persistent connection.
- `testutil.MockSSH` (`internal/testutil/mock_ssh.go`) — test mock. Canned responses by exact command or prefix match. Records all calls for assertions.

**Connection lifecycle:** One SSH connection per deploy. `Cluster.MasterSSH` (`utils.SSHClient`) is set once after `ServersAdd()`, shared across all subsequent operations via `borrowedSSH` (no-op Close). API dispatch path connects on-demand (no `MasterSSH`).

**Testing:** Set `Cluster.MasterSSH = &testutil.MockSSH{...}` to inject canned SSH responses. `MockSSH` matches commands by exact string first, then by prefix. Use `ssh.Calls` to assert which commands ran, `ssh.Uploads` to assert file uploads. See `internal/core/teardown_test.go` and `internal/core/database_test.go` for patterns.

`ComputeSet` connects to individual servers via `Cluster.Connect()` for provisioning (Docker, k3s, swap). Those are separate connections — not the master.

SSH errors: `ErrHostKeyChanged` and `ErrAuthFailed` surface immediately with guidance. Stale known hosts auto-cleared on server creation.

### Reconcile flow

```
Deploy(ctx, dc, cfg, viper)
  → ValidateConfig(cfg)              — includes package validation
  → cfg.Resolve()                    — populate VolumeDef.MountPath, DatabaseDef resolved names
  → DescribeLive(ctx, dc, cfg) → LiveState
  → ServersAdd(ctx, dc, cfg)          — create desired, NO orphan removal yet
  → establish MasterSSH
  → Firewall(ctx, dc, live, cfg)
  → Volumes(ctx, dc, live, cfg)
  → Build(ctx, dc, cfg)
  → Secrets(ctx, dc, live, cfg, v) → secretValues
  → packages.ReconcileAll(ctx, dc, cfg) → packageEnvVars
  → Storage(ctx, dc, live, cfg) → storageCreds
  → mergeSources(secretValues, packageEnvVars, storageCreds) → sources
  → Services(ctx, dc, live, cfg, sources)
  → Crons(ctx, dc, live, cfg, sources)
  → ServersRemoveOrphans(ctx, dc, live, cfg) — drain + delete AFTER workloads moved
  → DNS(ctx, dc, live, cfg)
  → Ingress(ctx, dc, live, cfg)
```

### Database package

`database:` in config triggers the database package. Per database:
1. Detect engine from image (postgres, mysql)
2. Read credentials from `DeployContext.DatabaseCreds` (required, no auto-generation)
3. Store as k8s Secret (`db.SecretName`)
4. Apply StatefulSet + headless Service (HostPath = `db.VolumeMountPath`)
5. Wait for readiness probe
6. Create backup bucket (`db.BackupBucket`)
7. Store backup bucket creds in per-cron secret (`db.BackupCredSecret`)
8. Apply backup CronJob (`db.BackupCronName`)
9. Return env vars as `$VAR` resolution sources for downstream services

Env vars returned (for database named `main` with postgres):
```
MAIN_DATABASE_URL, MAIN_POSTGRES_USER, MAIN_POSTGRES_PASSWORD,
MAIN_POSTGRES_DB, MAIN_POSTGRES_HOST, MAIN_POSTGRES_PORT
```

Services consume these via `secrets:` with `$VAR` resolution (e.g. `DATABASE_URL=$MAIN_DATABASE_URL`). They are NOT auto-injected — services must explicitly declare them.

All database resource names are resolved once in `config.Resolve()` from `pkg/utils/naming.go` functions. Consumers read `DatabaseDef` fields — never inline concatenation. See `internal/packages/database/CLAUDE.md` for the full naming table.

Package-managed resources are protected from orphan detection via resolved fields: `db.ServiceName`, `db.BackupCronName`, `db.BackupBucket`.

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

## Working tree

The working tree frequently has uncommitted changes — that's normal. The on-disk file is always the intended version. When reviewing, never flag a mismatch between a prior commit and the working tree as a bug. The working tree is the source of truth. Commits happen when the user asks.

## Key rules

1. `app` + `env` in `nvoi.yaml` are required. They're the namespace for everything.
2. No state files. Infrastructure is the truth.
3. `deploy` is idempotent. Run twice, same result.
4. `teardown` nukes external provider resources. Volumes and storage preserved by default.
5. Provider interfaces scale. Add a provider = implement the interface.
6. Naming: `nvoi-{app}-{env}-{resource}`. Deterministic. No UUIDs.
7. SSH keys injected via cloud-init only. Single SSH connection per deploy (`MasterSSH`).
8. **`os.Getenv` lives exclusively in `cmd/`.** `internal/`, `pkg/` never read env vars. `cmd/cli/local.go` resolves from env vars, `cmd/api/` resolves from the database. Both produce the same `DeployContext`.
9. **Providers are silent.** Never print or narrate. Output via `pkg/core/` → `Output` interface.
10. **`pkg/core/` never writes to stdout.** All output through `Output` interface.
17. **Every provider operation goes through `pkg/core/`.** No caller should invoke a provider method directly. `pkg/core/` wraps every operation with output, error handling, and naming resolution. Teardown, reconcile, CLI commands — all go through `pkg/core/` functions. Direct provider calls bypass output and error reporting.
11. **`pkg/core/` never imports `net/http`.** HTTP calls belong in `infra/` or `provider/`.
12. **Errors flow up, render once.** `pkg/core/` returns errors. Cobra renders via `Output.Error()`.
13. **No shell injection.** Secret values via file upload, not inline interpolation.
14. **Web-facing services require replicas >= 2.** Omitted defaults to 2, explicit 1 is a hard error. This ensures process-level redundancy — 2 replicas on a single `server:` node is valid (zero-downtime rolling updates).
15. **Package-managed resources are protected from orphan detection.**
16. **Database credentials are user-owned.** No auto-generation. Missing = hard error.
18. **Input validated once at the boundary.** Config parse (`ValidateConfig`) and API input (`validateDispatchInput`) are the only places that validate user input. Internal code trusts validated input — no defensive escaping, no silent sanitization. `NewNames()` validates, not sanitizes. Validators: `ValidateName` (DNS-1123) for resource names, `ValidateEnvVarName` (POSIX) for secret keys, `ValidateDomain` for domains. All in `pkg/utils/naming.go`.
22. **Single binary, two modes via Backend interface.** `cmd/cli` is the only CLI. `--local` dispatches to `localBackend` (pkg/core directly); default cloud mode dispatches to `cloudBackend` (API relay). Commands are backend-agnostic — no mode branching in command files. Cloud-only commands (login, whoami, workspaces, repos, provider) hard-error with `--local`.

## CLI mode detection

Single binary (`cmd/cli`). Mode selected by flag or auth state.

- `--local` flag → **localBackend** (call `pkg/core/` with local provider creds). Reads `nvoi.yaml` + env vars.
- `~/.config/nvoi/auth.json` exists → **cloudBackend** (relay through API). Default when authenticated.
- No auth, no `--local` → error with guidance to authenticate or pass `--local`.
- `nvoi.yaml` present but no auth → suggest both options.

19. **One kubectl primitive: `kctl(ns, cmd)`.** Every kubectl-over-SSH call in `pkg/kube/` goes through this single unexported helper. `ns=""` for cluster-scoped, `ns="foo"` for namespaced. YAML applied via SFTP upload + `kctl`, never heredocs. No code outside `pkg/kube/` constructs kubectl strings. Exception: `pkg/infra/k3s.go` bootstrap uses `sudo k3s kubectl` before deploy-user kubeconfig exists.
20. **Async provider actions polled to completion.** Every action that returns an ID must be polled via `waitForAction` before proceeding. Fire-and-forget = production race condition.
21. **`DeleteServer` detaches firewall before termination.** Every provider. Hetzner: `detachFirewall` + poll. AWS: move to VPC default SG. Scaleway: reassign to project default SG. `DeleteFirewall` retries "still in use."

## Production hardening notes

- **`~` doesn't expand in Go.** `resolveSSHKey()` calls `expandHome()`.
- **`kubectl apply` does strategic merge, not full replace.** `kube.Apply()` uses `kubectl replace` first, falls back to `kubectl apply --server-side --force-conflicts`.
- **Ingress uses k3s built-in Traefik.** Standard k8s Ingress resources, one per service. No custom ingress controller deployment.
- **DNS and ingress are separate concerns.** DNS creates A records. Ingress creates k8s Ingress resources.
- **HTTPS verification is two-step.** Step 1: check ACME cert exists in Traefik's acme.json. Step 2: curl from server verifies service responds (any non-5xx). Both run via SSH — no DNS propagation dependency.
- **SSH host key changed = hard error** with guidance to clear known hosts. Auto-cleared on server creation.
- **Firewall never reset during server creation.** `ensureFirewall` only ensures existence.
- **Concurrency control on deploy workflows.** `concurrency: { group: deploy, cancel-in-progress: false }`.
- **Root disk size is creation-only.** `disk` in server config applies at `EnsureServer` time. Changing it on an existing server has no effect — `EnsureServer` returns the existing server as-is. Resize requires server recreation. Hetzner doesn't support custom root disk sizes at all (fixed per server type) — validated at config time.
