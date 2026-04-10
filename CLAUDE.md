# CLAUDE.md — nvoi

## What nvoi is

A CLI that deploys containers to cloud servers from a declarative YAML config. `nvoi deploy` reconciles live infrastructure to match the config. `nvoi destroy` tears it all down. `nvoi describe` fetches everything live from the cluster.

## Philosophy

- **`app` + `env` is the namespace.** Defined in `nvoi.yaml`. `nvoi-{app}-{env}-*`. Different app or env = brand new infrastructure.
- **No state files.** No manifest, no database, no local cache. Infrastructure is the source of truth.
- **Everything is idempotent.** `nvoi deploy` reconciles: adds desired resources, removes orphans. Run twice, same result.
- **Naming is the lookup key.** `nvoi-{app}-{env}-{resource}`. Deterministic. No UUIDs. The naming convention finds everything.
- **Declarative config, imperative reconciliation.** `nvoi.yaml` declares desired state. The reconciler walks each resource type in order: servers, firewall, volumes, build, secrets, storage, services, crons, DNS, ingress.
- **`describe` fetches everything live from the cluster.** Nodes, workloads, pods, services, ingress, secrets, storage — all via kubectl over SSH.
- **Provider interfaces scale.** Hetzner, Cloudflare, AWS, Scaleway. Interface-first. Add a provider = implement the interface.
- **SSH is the transport.** No agent binary. SSH in, run commands, done.
- **Secrets are k8s secrets.** Values live in the cluster only. Resolved from environment variables at deploy time.
- **Storage credentials are k8s secrets.** `storage` creates the bucket AND stores S3 credentials in the cluster. `storage:` on a service injects them.

## Build & Test

```bash
go test ./...
go test ./... -v
go test ./... -run TestWaitRollout
go test ./... -cover
go build ./cmd/core
```

Tests in three tiers:
- **Tier 1** — pure functions: naming, YAML generation, Caddyfile, Poll, credential validation, volume parsing, signed URLs, route merging, cloud-init (hostname), APIError, AWS ArchForType, instanceFromEC2, volumeFromEC2, nvoiTags, deref helpers, config validation, config parsing
- **Tier 2** — mock SSH: kubectl secret ops, Apply, DeleteByName, FirstPod, FindMaster, describe parsers, k3s install, registry, Docker, volume mount/unmount, reconcile orchestration
- **Tier 3** — httptest: Hetzner API (servers, volumes, firewalls, networks, auth), Cloudflare API (buckets, DNS records, credentials), AWS provider resolution (compute, DNS, missing creds), API handler tests (auth, workspaces, repos, SSH, describe, query)

## CI

Five GitHub Actions workflows (`.github/workflows/`):

- **ci.yml** — fmt + vet + test + build on push and PR
- **deploy.yml** — production deploy on push to main (builds `cmd/core`, runs `bin/deploy`)
- **release.yml** — cross-compile on git tags (`v*`), upload to R2 via `cmd/distribution/upload.go`
- **claude.yml** — Claude Code integration on `@claude` mentions
- **claude-code-review.yml** — automatic code review on PR opened

**PR merges:** Never squash. Use `gh pr merge --merge --delete-branch`. Each commit is a meaningful change — preserve the history.

## Local development

```bash
bin/core deploy                                  # deploy from nvoi.yaml
bin/core destroy                                 # destroy all resources
bin/core describe                                # live cluster state
bin/cloud login                                  # cloud CLI (go run ./cmd/cli)
go test ./...                                    # run tests
```

### First run

```bash
cp examples/.env.example examples/.env  # fill in credentials
cd examples && ../bin/core deploy --config hetzner.yaml
```

### Files

| File | Purpose |
|------|---------|
| `.env` | Platform deploy env (not tracked) |
| `examples/.env` | Example/dev env (not tracked) |
| `examples/.env.example` | Template for example env |
| `examples/hetzner.yaml` | Example deploy config — Hetzner compute |
| `examples/aws.yaml` | Example deploy config — AWS compute |
| `examples/scaleway.yaml` | Example deploy config — Scaleway compute |
| `bin/core` | Direct CLI — `go run ./cmd/core` |
| `bin/cloud` | Cloud CLI — `go run ./cmd/cli` |
| `bin/dev` | Website development loop |
| `bin/nvoi` | Cached `cmd/core` binary wrapper |
| `bin/deploy` | Platform self-deploy (granular commands) |
| `bin/destroy` | Platform self-destroy (granular commands) |

## Config format

Everything is one YAML file. Provider credentials come from environment variables.

```yaml
app: dummy-rails
env: production

providers:
  compute: hetzner          # hetzner | aws | scaleway
  dns: cloudflare           # cloudflare | aws | scaleway
  storage: cloudflare       # cloudflare | aws
  build: daytona            # local | daytona | github

servers:
  master:
    type: cx23
    region: fsn1
    role: master             # exactly one master required
  worker:                    # optional workers
    type: cx33
    region: fsn1
    role: worker

firewall: default            # string or list of port:cidr rules

volumes:
  pgdata:
    size: 20                 # GB
    server: master           # must reference a defined server

build:
  web: benbonnet/dummy-rails # name: git source

secrets:                     # resolved from env vars at deploy time
  - RAILS_MASTER_KEY
  - POSTGRES_PASSWORD

storage:
  assets:
    cors: true               # optional
    expire_days: 30           # optional
    bucket: custom-name       # optional override

services:
  db:
    image: postgres:17       # image or build, mutually exclusive
    port: 5432
    volumes: ["pgdata:/var/lib/postgresql/data"]
    secrets: [POSTGRES_PASSWORD]
  web:
    build: web               # references build target
    port: 80
    replicas: 2
    health: /up
    env: [RAILS_ENV=production, POSTGRES_HOST=db]
    secrets: [POSTGRES_PASSWORD, RAILS_MASTER_KEY]
    storage: [assets]
    server: master            # optional — pin to node

crons:
  cleanup:
    build: web
    schedule: "0 1 * * *"
    command: bin/cleanup

domains:
  web: [myapp.com, www.myapp.com]
```

### Validation

`ValidateConfig()` runs before touching infrastructure. Fail-fast — returns first error:

- `app` and `env` required
- `providers.compute` required
- At least one server, exactly one master, all have type/region/role
- Volumes: size > 0, server exists
- Services/crons: image XOR build, referenced build/storage/volumes exist
- Volume mounts: `name:/path` format, volume must be on same server as workload
- Domains: service exists and has a port

## Commands

```bash
nvoi deploy                  # reconcile infrastructure to match config
nvoi destroy                 # tear down all resources in config
nvoi describe                # live cluster state
nvoi resources               # list all provider resources
nvoi logs <service>          # stream service logs
nvoi logs <service> -f       # follow logs
nvoi exec <service> -- cmd   # run command in service pod
nvoi ssh -- cmd              # run command on master node
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
  distribution/upload.go   CI binary upload tool

pkg/                       Public library — the execution engine
  core/                    Business logic. One file per domain. No cobra, no I/O, no stdout.
    cluster.go             Cluster struct + ProviderRef type
    output.go              Output interface + JSONL event types (Event, MarshalEvent, ParseEvent, ReplayEvent)
    compute.go             ComputeSet, ComputeDelete, ComputeList
    service.go             ServiceSet, ServiceDelete
    dns.go                 DNSSet, DNSDelete, DNSList
    ingress.go             IngressSet, IngressDelete
    storage.go             StorageSet, StorageDelete, StorageEmpty, StorageList
    secret.go              SecretSet, SecretDelete, SecretList, SecretReveal
    volume.go              VolumeSet, VolumeDelete, VolumeList
    build.go               BuildRun, BuildParallel, BuildList, BuildLatest, BuildPrune
    cron.go                CronSet, CronDelete
    describe.go            Describe, DescribeJSON — live cluster state
    resources.go           Resources — all provider resources
    firewall.go            FirewallSet, FirewallList
    wait.go                WaitRollout — polls pods with terminal failure detection
    exec.go                Exec — kubectl exec in pod
    ssh.go                 SSH — run command on master
    logs.go                Logs — stream pod logs
  kube/                    K8s YAML generation + kubectl over SSH + Caddy ingress + rollout
  infra/                   SSH, server bootstrap, k3s, Docker, volume mounting
  provider/                ComputeProvider + DNSProvider + BucketProvider + BuildProvider interfaces
    hetzner/               Hetzner Cloud (compute + volumes)
    cloudflare/            Cloudflare (DNS + R2 buckets)
    aws/                   AWS (EC2 + VPC + Route53 + S3)
    scaleway/              Scaleway (compute + DNS)
    daytona/               Daytona remote sandbox builds
    github/                GitHub Actions builds
    local/                 Local docker buildx builds
  utils/                   Pure utilities: naming, poll, httpclient, ssh keys, format, maps, params
    s3/                    S3-compatible operations with AWS Signature V4

internal/                  Private
  reconcile/               Deploy/destroy orchestrator — YAML to infrastructure
    schema.go              AppConfig, ProvidersDef, ServerDef, ServiceDef, CronDef, etc.
    context.go             DeployContext, LiveState
    reconcile.go           Deploy() — ordered reconciliation: servers → firewall → volumes → build → secrets → storage → services → crons → DNS → ingress
    validate.go            ValidateConfig() — fail-fast pre-flight checks
    helpers.go             DescribeLive(), SplitServers(), orphan detection
    servers.go             Server reconciliation (add desired, drain + remove orphans)
    firewall.go            Firewall reconciliation
    volumes.go             Volume reconciliation
    build.go               Build reconciliation
    secrets.go             Secret reconciliation (resolved from viper/env)
    storage.go             Storage reconciliation
    services.go            Service reconciliation (resolve images, wait rollout)
    crons.go               Cron reconciliation
    dns.go                 DNS reconciliation
    ingress.go             Ingress reconciliation
  core/                    Direct CLI commands + env resolution
    deploy.go              NewDeployCmd — load YAML, call reconcile.Deploy()
    destroy.go             NewDestroyCmd — load YAML, cascade destroy
    describe.go            NewDescribeCmd, NewResourcesCmd
    logs.go                NewLogsCmd
    exec.go                NewExecCmd
    ssh.go                 NewSSHCmd
    resolve.go             BuildContext() — viper + env vars → DeployContext
  cli/                     Cloud CLI — HTTP relay to API
    backend.go             deploy/destroy (send YAML, stream JSONL), describe, resources, logs, exec, ssh
    root.go                Root cobra command + standalone commands
    client.go              APIClient (doRaw, doRawWithBody, parseAPIError)
    auth.go                Auth config (~/.config/nvoi/auth.json)
    login.go               GitHub token → JWT flow
    provider.go            nvoi provider set/list/delete
    repos.go               nvoi repos create/list/use/delete
    workspaces.go          nvoi workspaces create/list/use/delete
    whoami.go              nvoi whoami
  api/                     REST API server (Huma + Gin + GORM)
    models.go              User, Workspace, WorkspaceUser, InfraProvider, Repo, CommandLog
    db.go                  PostgreSQL + AutoMigrate
    encrypt.go             AES-256-GCM for secrets at rest
    jwt.go                 HS256 JWT, 30-day TTL
    auth.go                AuthRequired middleware
    github.go              GitHub token verification
    handlers/router.go     Huma route registration
    handlers/run.go        POST /run — dispatch to pkg/core/, stream JSONL
    handlers/query.go      Read-only endpoints (instances, volumes, dns, secrets, storage, builds, logs, exec)
    handlers/describe.go   Describe + Resources
    handlers/ssh.go        POST /ssh
    handlers/auth.go       POST /login
    handlers/workspaces.go CRUD workspaces
    handlers/repos.go      CRUD repos
    handlers/providers.go  Set/list/delete providers
  render/                  Output renderers — TUI, Plain, JSON, Table, Describe, Resources, Delete
  testutil/                MockSSH, MockCompute, MockDNS, MockBucket, MockOutput
```

### Two CLIs, same pkg/core/

Both CLIs call `pkg/core/` functions — no shared Backend interface. The direct CLI calls them in-process via the reconcile engine. The cloud CLI sends the YAML to the API, which dispatches to `pkg/core/` and streams JSONL back.

- **Direct CLI** (`internal/core/`) — reads `nvoi.yaml` + env vars, builds `DeployContext`, calls `reconcile.Deploy()` or `pkg/core/` directly
- **Cloud CLI** (`internal/cli/`) — sends YAML to API endpoints, renders streamed JSONL through TUI

Both produce identical output. Same lipgloss, same events, same look.

### Reconcile flow

```
nvoi deploy --config nvoi.yaml
  → loadConfig() → ParseAppConfig(yaml)
  → BuildContext(cmd) → DeployContext (cluster + provider refs from env)
  → reconcile.Deploy(ctx, dc, cfg, viper)
    → ValidateConfig(cfg)
    → DescribeLive(ctx, dc) → LiveState (current cluster + provider state)
    → Servers(ctx, dc, live, cfg)   — add desired, drain + remove orphans
    → Firewall(ctx, dc, live, cfg)
    → Volumes(ctx, dc, live, cfg)
    → Build(ctx, dc, cfg)
    → Secrets(ctx, dc, live, cfg, v) — resolved from env vars
    → Storage(ctx, dc, live, cfg)
    → Services(ctx, dc, live, cfg)  — resolve images, wait rollout
    → Crons(ctx, dc, live, cfg)
    → DNS(ctx, dc, live, cfg)
    → Ingress(ctx, dc, live, cfg)
```

### Cloud CLI flow

```
nvoi deploy (cloud CLI)
  → POST /workspaces/{wid}/repos/{rid}/deploy {config: yamlString}
    → API dispatches to reconcile engine
    → streams JSONL output back
  → CLI renders JSONL through TUI
```

The API also supports granular operations via `POST /run {kind, name, params}` for individual resource mutations (instance.set, service.delete, etc.).

## Providers

Everything pluggable is a provider. Same pattern for all four kinds: interface + credential schema + `init()` register + factory.

| Kind | YAML key | Env var | Interface | Implementations |
|------|----------|---------|-----------|----------------|
| Compute | `providers.compute` | `COMPUTE_PROVIDER` | `ComputeProvider` | hetzner, aws, scaleway |
| DNS | `providers.dns` | `DNS_PROVIDER` | `DNSProvider` | cloudflare, aws, scaleway |
| Storage | `providers.storage` | `STORAGE_PROVIDER` | `BucketProvider` | cloudflare (R2), aws (S3) |
| Build | `providers.build` | `BUILD_PROVIDER` | `BuildProvider` | local, daytona, github |

See [`pkg/provider/CLAUDE.md`](pkg/provider/CLAUDE.md) for registration pattern, credential resolution, and `.env` reference.

## Validation guardrails

`ValidateConfig()` runs hard errors before touching infrastructure:

- **Identity:** `app` and `env` required
- **Servers:** at least one server, exactly one master, all have type/region/role
- **Volumes:** size > 0, server reference exists, mount format is `name:/path`
- **Services/Crons:** image XOR build (mutually exclusive), referenced build/storage/volumes exist
- **Volume placement:** workload and volume must be on the same server
- **Domains:** service exists and has a port, at least one domain per entry
- **Build:** source required for every build target

Runtime guardrails in `pkg/core/`:
- **Cluster:** master must exist. k3s must be installed.
- **Node labeling:** ComputeSet labels nodes `nvoi-role={name}`. `server:` on services matches via `nodeSelector`.
- **Placement:** `server:` pins to node. Volume services auto-pinned to volume's server.
- **Volumes:** refs checked via provider API. Volume → StatefulSet, replicas=1.
- **DNS / Ingress:** DNS creates A records. Ingress deploys Caddy with `hostNetwork`, `Recreate` strategy. TLS is ACME only.
- **Storage:** creates bucket + stores S3 creds as 4 k8s secrets. `storage:` on services expands to secret refs.
- **Secrets:** referenced secrets must exist in the cluster (hard error if missing).
- **Rollout:** polls pods with live feedback. Terminal states (`CrashLoopBackOff`, `ImagePullBackOff`, `OOMKilled`) exit immediately with logs.

## Output contract

**Providers are silent. `pkg/core/` narrates. `internal/render/` renders. No exceptions.**

- `pkg/core/` communicates through the `Output` interface (Command, Progress, Success, Warning, Info, Error, Writer)
- `pkg/core/` returns errors. Never renders them. Never calls `Output.Error()`.
- Cobra handles all errors via `root.SetErr()` → `Output.Error()`. Single path.
- Three renderers: TUI (terminal), JSONL (`--json`), Plain (`--ci` or non-TTY)
- Both CLIs produce identical output. Direct CLI calls `pkg/core/` → `Output`. Cloud CLI replays JSONL from the API through the same renderers.

See [`internal/render/CLAUDE.md`](internal/render/CLAUDE.md) for renderer details, JSONL format, streaming.

## Key rules

1. `app` + `env` in `nvoi.yaml` are required. They're the namespace for everything.
2. No state files. Infrastructure is the truth. `describe` fetches live from the cluster.
3. `deploy` is idempotent. Run twice, same result. Adds desired state, removes orphans.
4. `destroy` is the reverse: tears down everything in the config, reverse order, tolerates missing resources.
5. Provider interfaces scale. Add a provider = implement the interface. Same registration pattern for all four kinds.
6. Naming: `nvoi-{app}-{env}-{resource}`. Deterministic. No UUIDs.
7. SSH is the only transport to remote servers. SSH keys are injected strictly via cloud-init UserData — never via provider SSH key APIs. `infra.RenderCloudInit` renders the public key into `ssh_authorized_keys`. This is the only key injection path.
8. **`os.Getenv` lives exclusively in `internal/core/`.** Environment variables are a CLI concept. `pkg/core/`, `provider/`, `infra/`, `utils/` never read env vars. All external values are resolved in `internal/core/resolve.go` and passed down as typed function arguments. Strictly enforced. No exceptions.
9. **Providers are silent.** Providers are API clients — they do work and return data. They never print, log, or narrate. Progress output belongs in `pkg/core/` via the `Output` interface.
10. **`pkg/core/` never writes to stdout.** No `fmt.Printf`, no `os.Stdout`, no `os` import for I/O. All output goes through the `Output` interface. `pkg/core/` is a library — the API handlers call the same functions.
11. **`pkg/core/` never imports `net/http`.** HTTP calls belong in `infra/` or `provider/`. `pkg/core/` is pure orchestration.
12. **Errors flow up, render once.** `pkg/core/` returns errors. Cobra renders them through `SetErr` → `Output.Error()`. Never double-print. Never swallow silently.
13. Every delete is idempotent. Deleting something that doesn't exist succeeds silently. Typed sentinel errors drive the rendering: `utils.ErrNotFound` → "already gone", `core.ErrNoMaster` → "cluster gone".
14. **No shell injection.** Secret values flow to kubectl via file upload (`ssh.Upload` + `cat`), not inline `fmt.Sprintf`. `shellQuote` for `--from-literal` args. Never interpolate user values into shell strings.
15. **All providers use `utils.HTTPClient`.** 30s default timeout. Consistent `APIError` types. `IsNotFound()` works uniformly. No raw `http.DefaultClient.Do()`. Exception: AWS provider uses AWS SDK v2 (its own HTTP transport).
16. **`internal/reconcile/` never reads env vars or imports `os`.** Config comes from `AppConfig` (YAML) + `DeployContext` (resolved in `internal/core/`). Secrets resolved from viper.

## Known limitations

- **No pagination on provider list operations.** Hetzner uses `per_page=50` for servers, volumes, firewalls, networks. No cursor continuation. Fine at current scale. Fix when adding multi-tenant.
- **No retry / backoff on transient HTTP errors.** Provider API calls fail immediately on 500s or connection drops. User re-runs the command. Idempotent deploy makes this safe.
- **`s3ops.go` uses a dedicated `s3Client` (not `utils.HTTPClient`).** S3/XML operations need raw HTTP, not JSON. By design.
- **AWS SDK `LoadDefaultConfig` errors are deferred to `ValidateCredentials`.** Provider factories can't return errors. AWS constructors store the config error and surface it on the first `ValidateCredentials` call.

## Production hardening notes

Lessons from real deployment failures.

- **`~` doesn't expand in Go.** `resolveSSHKey()` calls `expandHome()` before reading. Any path from env vars or flags that could contain `~` must expand it.
- **`kubectl apply` does strategic merge, not full replace.** `kube.Apply()` uses `kubectl replace` first, falls back to `kubectl apply --server-side --force-conflicts` for first creation.
- **Caddy with `hostNetwork` can't rolling-update on single-node.** Caddy Deployment uses `Recreate` strategy.
- **DNS and ingress are separate concerns.** DNS creates A records only. Ingress owns Caddy entirely — takes all `service:domain` mappings, builds full Caddyfile, deploys once.
- **Rollout must detect terminal failures.** `CrashLoopBackOff` and `Error` statuses trigger early exit. On bail-out, fetch `--previous` logs.
- **Build source and Dockerfile are separate.** Source `./cmd/web` means the Dockerfile lives at `./cmd/web/Dockerfile`. Build context is the project root.
- **GitHub Actions secrets can't start with `GITHUB_`.** Reserved prefix.
- **Concurrency control on deploy workflows.** Use `concurrency: { group: deploy, cancel-in-progress: false }` to serialize.
- **ARM servers need ARM runners.** `cax11` (Hetzner ARM) produces `linux/arm64` images. Use `ubuntu-24.04-arm` runner.
