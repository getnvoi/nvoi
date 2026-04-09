# CLAUDE.md — nvoi

## What nvoi is

A CLI that deploys containers to cloud servers. Granular commands hit real infrastructure. `nvoi describe` fetches everything live from the cluster.

## Philosophy

- **`NVOI_APP_NAME` + `NVOI_ENV` is the namespace.** `nvoi-{app}-{env}-*`. Different app or env = brand new infrastructure.
- **No state files.** No manifest, no database, no local cache. Infrastructure is the source of truth.
- **Everything is idempotent.** Every command hits real infrastructure — provider APIs over HTTP, servers over SSH, cluster via kubectl. Run twice, same result.
- **Naming is the lookup key.** `nvoi-{app}-{env}-{resource}`. Deterministic. No UUIDs. The naming convention finds everything.
- **Reconcile with `set`, remove with `delete`.** `instance set`, `volume set`, `dns set`, `service set`, `secret set`, `storage set` create or reconcile. `instance delete`, `volume delete`, `dns delete`, `service delete`, `secret delete`, `storage delete`, `ingress delete` remove explicitly.
- **`describe` fetches everything live from the cluster.** Nodes, workloads, pods, services, ingress, secrets, storage — all via kubectl over SSH.
- **Provider interfaces scale.** Hetzner, Cloudflare, AWS. Interface-first. Add a provider = implement the interface.
- **SSH is the transport.** No agent binary. SSH in, run commands, done.
- **Secrets are k8s secrets.** Values live in the cluster only.
- **Storage credentials are k8s secrets.** `storage set` creates the bucket AND stores S3 credentials in the cluster. `--storage` on `service set` injects them.

## Build & Test

Local tooling path:

```bash
docker compose run --rm --entrypoint sh core -c "go test ./..."
docker compose run --rm --entrypoint sh core -c "go test ./... -v"
docker compose run --rm --entrypoint sh core -c "go test ./... -run TestWaitRollout"
docker compose run --rm --entrypoint sh core -c "go test ./... -cover"
docker compose run --rm --entrypoint sh core -c "go build ./cmd/core"
```

Use `docker compose run --rm --entrypoint sh core -c '...'` directly for ad hoc test/build commands.

Tests in three tiers:
- **Tier 1** — pure functions: naming, YAML generation, Caddyfile, Poll, credential validation, volume parsing, signed URLs, route merging, cloud-init (hostname), APIError, AWS ArchForType, instanceFromEC2, volumeFromEC2, nvoiTags, defaultIngressRules, deref helpers
- **Tier 2** — mock SSH: WaitRollout terminal errors, kubectl secret ops, Apply, DeleteByName, FirstPod, FindMaster, describe parsers, k3s install, registry, Docker, volume mount/unmount
- **Tier 3** — httptest: Hetzner API (servers, volumes, firewalls, networks, auth), Cloudflare API (buckets, DNS records, credentials), AWS provider resolution (compute, DNS, missing creds)

## CI

Claude Code review runs on GitHub Actions (`.github/workflows/claude-code-review.yml`). Triggers:
- **PR opened** — automatic review on new pull requests
- **Manual** — `workflow_dispatch` from the Actions tab

Does not run on every push/sync to a PR. Manual trigger for re-reviews.

## Local development

Compose is for local tooling only. Real deploys use `bin/deploy` / `bin/destroy`, which call `bin/nvoi`.

```bash
bin/core instance list                           # direct CLI
bin/core describe                                # live cluster state
bin/cloud login                                  # cloud CLI (starts postgres + api automatically)
docker compose run --rm --entrypoint sh core -c "go test ./..."
```

**Compose handles the full dependency chain.** `bin/cloud login` starts postgres → waits healthy → starts api → waits healthy → runs cli. One command. Never start services manually.

See [`examples/README.md`](examples/README.md) for full deploy/destroy workflows across all providers.

### How it works

`bin/core` runs the direct CLI in the local compose `core` container. The compose service:

- Mounts source (`.:/app`) — changes picked up instantly, no rebuild
- Mounts SSH keys (`~/.ssh:/root/.ssh:ro`)
- Loads `examples/.env` via `env_file` — local/example credentials only
- Only overrides container-specific paths: `SSH_KEY_PATH=/root/.ssh/id_rsa`
- Caches Go modules across runs (Docker volumes)
- Entrypoints use `go run` — Go's build cache makes subsequent runs instant when source hasn't changed

**Env split is strict.**

- Root `.env` is for the real app deploy path only: `bin/deploy`, `bin/destroy`, GitHub deploy.
- `examples/.env` is for examples and local compose tooling only.

**Real deploy path is separate.**

- `bin/deploy` and `bin/destroy` do not go through compose.
- They call `bin/nvoi`, which builds/runs the real `cmd/core` binary locally.
- GitHub Actions uses that same path by running `./bin/deploy`.

### First run (local tooling / examples)

```bash
cp examples/.env.example examples/.env  # fill in example/dev credentials
bin/core instance set master --compute-type cx23 --compute-region fsn1
```

### Files

| File | Purpose |
|------|---------|
| `.env` | Real app deploy env only (not tracked) |
| `.env.example` | Template for real app deploy env |
| `examples/.env` | Example/dev env only (not tracked) |
| `examples/.env.example` | Template for example/dev env |
| `docker-compose.yml` | Local tooling stack: `core`, `api`, `cli`, `postgres` |
| `bin/core` | Direct CLI — wraps `docker compose run --rm core` |
| `bin/cloud` | Cloud CLI — wraps `docker compose run --rm cli` |
| `bin/dev` | Website development loop |
| `bin/entrypoint` | Compose entrypoint for `core` service |
| `bin/nvoi` | Real `cmd/core` binary wrapper for `bin/deploy` and `bin/destroy` |
| `bin/deploy` | Real app deploy entrypoint |
| `bin/destroy` | Real app destroy entrypoint |

See [`examples/README.md`](examples/README.md) for deploy workflows (direct + cloud mode).

## Namespace

Two values. Both required. Everything keys off them. Flag or env var — same result.

```bash
# Via env vars (example/local namespace)
export NVOI_APP_NAME=dummy-rails
export NVOI_ENV=production
# → nvoi-dummy-rails-production-master, nvoi-dummy-rails-production-fw, ...

# Via flags (overrides env vars)
nvoi instance list --app-name dummy-rails --environment staging
# → nvoi-dummy-rails-staging-master, nvoi-dummy-rails-staging-fw, ...
```

Different app or env = completely isolated infrastructure. Same commands, different resources.

## Commands

Every flag has an env var fallback. With env vars set, commands need zero provider flags.

```bash
# ── Flag → env var resolution ──────────────────────────────────────────────────
# --app-name         → NVOI_APP_NAME
# --environment      → NVOI_ENV
# --compute-provider → COMPUTE_PROVIDER
# --build-provider   → BUILD_PROVIDER
# --dns-provider     → DNS_PROVIDER
# --zone             → DNS_ZONE
# --storage-provider → STORAGE_PROVIDER
# --compute-credentials KEY=VAL → per-provider env vars (HETZNER_TOKEN, etc.)
# --build-credentials KEY=VAL  → per-provider env vars (DAYTONA_API_KEY, etc.)

# ── Infrastructure — instance set installs k3s (master by default, --worker to join)
nvoi instance set <name> --compute-type cx23 --compute-region fsn1
nvoi instance set <name> --compute-type cx33 --compute-region fsn1 --worker
nvoi instance delete <name>
nvoi instance list
nvoi volume set <name> --size 20 --server master
nvoi volume delete <name>
nvoi volume list

# ── DNS — DNS records only
# "web" is the service name — must have --port set via service set.
nvoi dns set <service> <domain...> --cloudflare-managed                         # explicit Cloudflare-managed overlay path
nvoi dns set <service> <domain...>                                              # direct DNS only
nvoi dns delete <service> <domain...>
nvoi dns list

# ── Ingress — Caddy routes/TLS only
# Caddy runs on master with hostNetwork and reverse-proxies to the k8s Service.
nvoi ingress apply <service:domain,domain ...>
nvoi ingress apply <service:domain,domain ...> --cloudflare-managed             # explicit Cloudflare-managed overlay path
nvoi ingress delete

# ── Storage — creates bucket, stores S3 credentials as k8s secrets
# Bucket name derived from convention: nvoi-{app}-{env}-{name}. Override with --bucket.
# Credentials stored as STORAGE_{NAME}_ENDPOINT, _BUCKET, _ACCESS_KEY_ID, _SECRET_ACCESS_KEY.
nvoi storage set <name>                                                         # creates bucket + secrets
nvoi storage set <name> --cors                                                  # with CORS enabled
nvoi storage set <name> --expire-days 30                                        # auto-expire objects
nvoi storage set <name> --bucket custom-name                                    # explicit bucket name
nvoi storage list                                                               # shows name + bucket
nvoi storage empty <name>                                                       # deletes all objects
nvoi storage delete <name>                                                      # deletes bucket + secrets

# Build — separate command, outputs image ref. Registry is the state.
nvoi build --build-provider local --source . --name web
nvoi build --build-provider daytona --source benbonnet/dummy-rails --name web
nvoi build --build-provider github --source benbonnet/dummy-rails --name web
nvoi build --build-provider github --source benbonnet/dummy-rails --name web --architecture arm64
nvoi build list
nvoi build latest <name>                                                        # returns image ref
nvoi build prune <name> --keep 3                                                # keep N, delete rest

# Secrets — stored in k8s, referenced by key name on service set.
nvoi secret set <key> <value>
nvoi secret delete <key>
nvoi secret list                                                                # keys only
nvoi secret reveal <key>                                                        # shows value

# Application — --image only. Build is a separate step.
# --secret KEY references a pre-existing secret (must exist, hard error if not).
# --secret KEY=VALUE is rejected — use secret set first.
# --storage <name> injects STORAGE_{NAME}_* env vars from secrets (repeatable for multiple buckets).
nvoi service set <name> --image postgres:17 --port 5432 --secret DB_PASSWORD
nvoi service set <name> --image $IMAGE --port 3000 --replicas 2 --secret RAILS_MASTER_KEY --storage assets
nvoi service delete <name>

# Cron — scheduled workloads (CronJob)
nvoi cron set <name> --image busybox --schedule "0 1 * * *" --command "echo hello"
nvoi cron set <name> --image postgres:17 --schedule "0 2 * * *" --storage db-backups --secret PGPASSWORD
nvoi cron delete <name>

# ── Managed databases ─────────────────────────────────────────────────────────
# --type is required. --secret reads from cluster. --backup-storage must pre-exist.
nvoi database set <name> --type postgres --secret POSTGRES_PASSWORD
nvoi database set <name> --type postgres --secret POSTGRES_PASSWORD --backup-storage db-backups --backup-cron "0 2 * * *"
nvoi database delete <name> --type postgres
nvoi database list
nvoi database backup create <name> --type postgres                              # trigger backup now
nvoi database backup list <name> --type postgres                                # list S3 objects
nvoi database backup download <name> --type postgres <artifact>                 # download to stdout

# ── Managed agents ────────────────────────────────────────────────────────────
# --type is required. --secret reads from cluster.
nvoi agent set <name> --type claude --secret NVOI_AGENT_TOKEN
nvoi agent delete <name> --type claude
nvoi agent list
nvoi agent exec <name> --type claude -- <command>
nvoi agent logs <name> --type claude

# Live view — nodes, workloads, pods, services, crons, ingress, secrets, storage
nvoi describe

# Operate
nvoi logs <service>                                                             # last 50 lines
nvoi logs <service> -f                                                          # follow/stream
nvoi logs <service> -n 100                                                      # last 100 lines
nvoi logs <service> --since 5m                                                  # last 5 minutes
nvoi logs <service> --previous                                                  # previous crashed container
nvoi logs <service> --timestamps                                                # with timestamps
nvoi exec <service> -- <command>                                                # run command in first pod
nvoi ssh <command>

# Inspect — queries all providers, shows every resource they created
nvoi resources                                                                  # tables per provider
nvoi resources --json                                                           # JSON output

# ── Fully explicit (no env vars) ──────────────────────────────────────────────
nvoi instance set master --compute-provider hetzner --compute-credentials HETZNER_TOKEN=xxx \
  --compute-type cx23 --compute-region fsn1 --app-name rails --environment production
```

See [`examples/README.md`](examples/README.md) for deploy/destroy workflows across all providers (direct + cloud mode).

## Architecture

```
cmd/
  core/main.go             Direct CLI entrypoint — signal handling, exit codes
  cli/main.go              Cloud CLI entrypoint — talks to API
  api/main.go              API server entrypoint

pkg/                       Public library — the execution engine
  core/                    Business logic. One file per domain. No cobra, no I/O, no stdout.
    cluster.go             Cluster struct + ProviderRef type
    output.go              Output interface — the contract between core/ and its viewers
    cron.go                CronSet, CronDelete — first-class CronJob primitive
    backup.go              BackupCreate (trigger job), BackupList (S3 list), BackupDownload (S3 get)
    managed_list.go        ManagedList — discovers managed services by nvoi/managed-kind label
  managed/                 Pure managed service compiler. One compiler, two interpreters (local + cloud).
    compiler.go            Definition interface (Kind, Category, Compile, Shape), registry, Compile(), Shape()
    types.go               Request, Result, Bundle, BundleShape, Operation, Ownership
    postgres.go            database/postgres — service + volume + secrets + optional backup cron
    claude.go              agent/claude — service + volume + secrets
  kube/                    K8s YAML generation + kubectl over SSH + Caddy ingress
    cron.go                CronJob YAML + CreateJobFromCronJob + hostPath support
  infra/                   SSH, server bootstrap, k3s, Docker, volume mounting, WaitHTTPS
  provider/                ComputeProvider + DNSProvider + BucketProvider + Builder interfaces
    hetzner/               Hetzner Cloud (compute + volumes)
    cloudflare/            Cloudflare (DNS + R2 buckets) — all via utils.HTTPClient
    aws/                   AWS (EC2 + VPC + Route53 + S3) — uses AWS SDK v2
    daytona/               Daytona remote builds
    github/                GitHub Actions builds
    local/                 Local docker buildx builds
  utils/                   Pure utilities: naming, poll, httpclient, ssh keys, format, maps, params
    ssh.go                 SSHClient interface + RemoteFileInfo
    sshutil.go             Ed25519 key generation (GenerateEd25519Key) + DerivePublicKey
    params.go              Typed extractors for map[string]any (GetString, GetInt, GetBool, GetStringSlice)
    maps.go                SortedKeys, RemovedKeys, ReverseSorted
    s3/                    AWS Signature V4 signing + ListObjects for S3-compatible APIs

internal/                  Private
  render/                  Shared renderers — TUI, Plain, JSON, Table, Resolve, ReplayLine, Delete, Describe, Resources
  testutil/                MockSSH, MockCompute, MockDNS, MockBucket, MockOutput
  core/                    Direct CLI. Cobra wrappers. Parse flags → call pkg/core/ → render via internal/render/
    database.go            nvoi database set/delete/list/backup — managed database category
    agent_cmd.go           nvoi agent set/delete/list/exec/logs — managed agent category
    managed.go             Shared helpers: resolveCluster, execOperation, deleteByShape, verifyManagedKind
    cron.go                nvoi cron set/delete
  api/                     REST API server — see [internal/api/CLAUDE.md](internal/api/CLAUDE.md)
    plan/resolve.go        ResolveDeploymentSteps — compiles managed bundles + wraps plan.Build() for cloud
  cli/                     Cloud CLI — login, deploy, stream logs via internal/render/ — see [internal/cli/README.md](internal/cli/README.md)
```

### Shared layers

| Layer | Used by | Purpose |
|-------|---------|---------|
| `pkg/core/` | `internal/core/`, `internal/api/` | Business logic — ComputeSet, ServiceSet, DNSSet, etc. |
| `pkg/core/output.go` | all | Output interface + JSONL event types (Event, MarshalEvent, ParseEvent, ReplayEvent) |
| `internal/render/` | `internal/core/`, `internal/cli/` | TUI, Plain, JSON renderers + Table + ReplayLine. Same formatting everywhere. |
| `pkg/utils/` | all | Naming, polling, HTTP client, maps, format |

Both CLIs produce identical output. The direct CLI calls `pkg/core/` → `Output` → `internal/render/`. The cloud CLI reads JSONL from the API → `render.ReplayLine()` → same `internal/render/`. Same lipgloss, same events, same look.

## Providers

Everything pluggable is a provider. Same pattern for all four kinds: interface + credential schema + `init()` register + factory.

| Kind | Flag | Env var | Interface | Implementations |
|------|------|---------|-----------|----------------|
| Compute | `--compute-provider` | `COMPUTE_PROVIDER` | `ComputeProvider` | hetzner, aws, scaleway |
| DNS | `--dns-provider` | `DNS_PROVIDER` | `DNSProvider` | cloudflare, aws, scaleway |
| Storage | `--storage-provider` | `STORAGE_PROVIDER` | `BucketProvider` | cloudflare (R2), aws (S3) |
| Build | `--build-provider` | `BUILD_PROVIDER` | `BuildProvider` | local, daytona, github |

See [`pkg/provider/CLAUDE.md`](pkg/provider/CLAUDE.md) for registration pattern, credential resolution, credential pairs, and `.env` reference.

## Managed services

Managed services are compiled by `pkg/managed` — a pure, shared compiler that produces deterministic bundles of primitive operations. Two categories, each with concrete kinds:

| Category | Kind | Config YAML | CLI |
|----------|------|-------------|-----|
| database | postgres | `managed: postgres` | `nvoi database set db --type postgres` |
| agent | claude | `managed: claude` | `nvoi agent set coder --type claude` |

### How it works

`pkg/managed.Compile(req)` takes a kind, name, and env (credential values) and returns a `Bundle` containing owned operations. The compiler is pure — same input, same output.

Two interpreters consume the same compiler output:

- **Local CLI:** `database set` reads secrets from cluster via `--secret`, compiles bundle, iterates operations, calls `pkg/core` functions directly.
- **Cloud API:** `plan.ResolveDeploymentSteps()` compiles bundles, strips managed-owned resources from config, calls `plan.Build()` for non-managed resources, merges into ordered steps, persists for deferred execution.

### Credential model

Credentials are required input. No generation, no persistence, no magic.

```bash
nvoi secret set POSTGRES_PASSWORD s3cret                    # store in cluster first
nvoi database set db --type postgres --secret POSTGRES_PASSWORD  # reads value from cluster
```

Missing credential = hard error: `managed postgres "db": missing required credential POSTGRES_PASSWORD`

### Backup pipeline

Backups use pre-existing storage (operator creates the bucket) and a pre-built backup image (`nvoi-pg-backup:{version}`) with pg_dump + aws cli baked in.

```bash
nvoi storage set db-backups --expire-days 30                # prerequisite: create bucket
nvoi database set db --type postgres \
  --secret POSTGRES_PASSWORD \
  --backup-storage db-backups \                             # verified to exist before compile
  --backup-cron "0 2 * * *"                                 # CronJob: pg_dump | gzip | aws s3 cp
```

The backup image is built once on the server (`FROM postgres:{version} + awscli`), pushed to the cluster registry, and shared across databases on the same postgres version. Storage credentials are injected as env vars from k8s secrets created by `storage set`.

### Category/kind model

`--type` is required on all category commands. No defaults.

```
Error: --type is required. Available database types: postgres
Error: --type is required. Available agent types: claude
```

Adding a new kind = one file in `pkg/managed/` implementing `Definition` (Kind, Category, Compile, Shape). It shows up in `--type` error messages automatically via `KindsForCategory()`.

### Delete uses Shape (no credentials needed)

`database delete db --type postgres` calls `managed.Shape("postgres", "db")` which returns owned names (service, volume, cron, secrets) without needing credential values. No cluster read before deletion.

### Discovery

Managed services are labeled with `nvoi/managed-kind` in k8s. `database list` and `agent list` query by this label. `verifyManagedKind` guards targeted commands (exec, logs, backup) — rejects non-managed or wrong-category services.

## Apply guardrails

Hard errors before touching k8s.

- **Cluster:** master must exist. k3s must be installed.
- **Services:** `--image` required. Build is separate (`build` outputs image ref, `service set` consumes it).
- **Cron:** `--image` and `--schedule` required. Reuses service conventions (secrets, storage, volumes, node selector).
- **Build:** `--compute-provider` + `--build-provider` + `--source` + `--name` required. Local path + remote builder = error. Remote repo + local builder = error. Registry is the state.
- **Node labeling:** `instance set` labels nodes `nvoi-role={name}`. `--server` on `service set` matches via `nodeSelector`.
- **Placement:** `--server` pins to node. Managed volume services pinned to volume's server.
- **Volumes:** refs must exist (checked via provider API). Managed volume → StatefulSet, replicas=1. `volume delete` unmounts then deletes.
- **DNS / Ingress:** `dns set` creates A records. `ingress set` deploys Caddy with `hostNetwork`. TLS mode per-deployment (ACME or cloudflare-managed, never mixed). `ingress delete` with `--cloudflare-managed` revokes Origin CA cert. Service must have `port > 0`.
- **Storage:** `storage set` creates bucket + stores S3 creds as 4 k8s secrets. `--storage` on `service set` expands to secret refs.
- **Secrets:** `--secret KEY` references pre-existing secret (hard error if missing). `--secret ENV=KEY` aliases. `secret delete` is idempotent. All credentials should be opaque hex.
- **Rollout:** polls pods with live feedback. Terminal states (`CrashLoopBackOff`, `ImagePullBackOff`, `OOMKilled`) exit immediately with logs.

## Output contract

**Providers are silent. `pkg/core/` narrates. `internal/core/` renders. No exceptions.**

- `pkg/core/` communicates through the `Output` interface (Command, Progress, Success, Warning, Info, Error, Writer)
- `pkg/core/` returns errors. Never renders them. Never calls `Output.Error()`.
- Cobra handles all errors via `root.SetErr()` → `Output.Error()`. Single path.
- Three renderers: TUI (terminal), JSONL (`--json`), Plain (`--ci` or non-TTY)
- Both CLIs produce identical output. Direct CLI calls `pkg/core/` → `Output`. Cloud CLI replays JSONL from the API through the same renderers.

See [`internal/render/CLAUDE.md`](internal/render/CLAUDE.md) for renderer details, JSONL format, streaming, Cluster/ProviderRef types.

## Key rules

1. `NVOI_APP_NAME` + `NVOI_ENV` (or `--app-name` + `--environment`) are required. They're the namespace for everything.
2. No state files. Infrastructure is the truth. `describe` fetches live from the cluster.
3. Everything is `set`. Idempotent. Run twice, same result. Deploy scripts run end to end, always same outcome.
4. `set` writes directly to infrastructure. No intermediate files.
5. Provider interfaces scale. Add a provider = implement the interface. Same registration pattern for all four kinds.
6. Naming: `nvoi-{app}-{env}-{resource}`. Deterministic. No UUIDs.
7. SSH is the only transport to remote servers. SSH keys are injected strictly via cloud-init UserData — never via provider SSH key APIs (e.g. Hetzner `ssh_keys`, AWS `KeyName`). `infra.RenderCloudInit` renders the public key into `ssh_authorized_keys`. This is the only key injection path.
8. **`os.Getenv` lives exclusively in `internal/core/`.** Environment variables are a CLI concept. `pkg/core/`, `provider/`, `infra/`, `utils/` never read env vars. All external values (credentials, SSH key path, app name, env) are resolved in `internal/core/resolve.go` and passed down as typed function arguments. Strictly enforced. No exceptions.
9. **Providers are silent.** Providers are API clients — they do work and return data. They never print, log, or narrate. Progress output belongs in `pkg/core/` via the `Output` interface.
10. **`pkg/core/` never writes to stdout.** No `fmt.Printf`, no `os.Stdout`, no `os` import for I/O. All output goes through the `Output` interface. `pkg/core/` is a library — the API handlers call the same functions.
11. **`pkg/core/` never imports `net/http`.** HTTP calls belong in `infra/` (e.g. `WaitHTTPS`) or `provider/`. `pkg/core/` is pure orchestration.
12. **Errors flow up, render once.** `pkg/core/` returns errors. Cobra renders them through `SetErr` → `Output.Error()`. Never double-print. Never swallow silently.
13. Every `delete` command is idempotent. Deleting something that doesn't exist succeeds silently. Typed sentinel errors drive the rendering: `utils.ErrNotFound` (resource gone at provider) → "already gone", `core.ErrNoMaster` (no cluster to clean up) → "cluster gone". `internal/render/delete.go` `HandleDeleteResult()` dispatches these. All 14 provider delete functions return `ErrNotFound` on 404 (not nil).
14. `examples/core/destroy` is the reverse of `examples/core/deploy`. Same commands, `delete` instead of `set`, reverse order. Tolerates missing resources — always runs to completion.
15. **No shell injection.** Secret values flow to kubectl via file upload (`ssh.Upload` + `cat`), not inline `fmt.Sprintf`. `shellQuote` for `--from-literal` args. Never interpolate user values into shell strings.
16. **All providers use `utils.HTTPClient`.** 30s default timeout. Consistent `APIError` types. `IsNotFound()` works uniformly. No raw `http.DefaultClient.Do()`. Exception: AWS provider uses AWS SDK v2 (its own HTTP transport).

## Known limitations

- **No pagination on provider list operations.** Hetzner uses `per_page=50` for servers, volumes, firewalls, networks. No cursor continuation. If results exceed one page, the list is silently incomplete — it doesn't error, it lies. Fine at current scale (1-5 servers per app). Fix when adding multi-tenant or `resources` across many apps on one Hetzner account.
- **No retry / backoff on transient HTTP errors.** Provider API calls fail immediately on 500s or connection drops. User re-runs the command. Idempotent `set` design makes this safe. Fix if deploy reliability becomes a problem.
- **`s3ops.go` uses a dedicated `s3Client` (not `utils.HTTPClient`).** S3/XML operations need raw HTTP, not JSON. Uses `var s3Client = &http.Client{Timeout: 30 * time.Second}` with all `io.ReadAll` errors checked and context propagated. This is by design — `utils.HTTPClient` is JSON-oriented.
- **AWS SDK `LoadDefaultConfig` errors are deferred to `ValidateCredentials`.** Provider factories can't return errors (signature is `func(creds) Provider`). The AWS constructors store the config error on the struct and surface it on the first `ValidateCredentials` call.

## Production hardening notes

Lessons from real deployment failures. Each was a bug, each was fixed, each teaches a pattern.

- **`~` doesn't expand in Go.** `os.ReadFile("~/.ssh/id_rsa")` fails. `resolveSSHKey()` calls `expandHome()` before reading. Any path from env vars or flags that could contain `~` must expand it. Never trust shell expansion in Go code.
- **`kubectl apply` does strategic merge, not full replace.** Switching a Deployment from `RollingUpdate` to `Recreate` via `kubectl apply` leaves the old `rollingUpdate` field, causing k8s to reject the update or silently ignore the strategy change. Fix: `kube.Apply()` uses `kubectl replace` first (full overwrite, no leftover fields), falls back to `kubectl apply --server-side --force-conflicts` for first creation.
- **Caddy with `hostNetwork` can't rolling-update on single-node.** New pod can't bind 80/443 while old pod holds them. Caddy Deployment uses `Recreate` strategy — kills old pod first, then starts new. No `RollingUpdate` for `hostNetwork` workloads on single-node clusters.
- **DNS and ingress are separate concerns.** `dns set` creates A records only. `ingress apply` owns Caddy entirely — takes all `service:domain` mappings, builds full Caddyfile, deploys once. Never restart Caddy per-domain. One deploy for all routes.
- **`WaitAllServices` must detect terminal failures.** Polling "1/2 pods ready" for 5 minutes with no feedback is useless. `CrashLoopBackOff` and `Error` statuses trigger early exit after `waitCrashTimeout` (2 min). On bail-out, fetch `--previous` logs from crashing pods via `kube.RecentLogs`. Always show WHY, not just WHAT.
- **`service set` in deploy scripts needs `--no-wait`.** Each `service set` calls `WaitAllServices` which checks ALL pods in the namespace — including unrelated crashing pods from previous failed deploys. Use `--no-wait` on all but the last service. Only the final service waits for the full cluster.
- **Build source and Dockerfile are separate.** `--source ./cmd/web` means the Dockerfile lives at `./cmd/web/Dockerfile`. Build context is the project root (walk up to `.git`/`go.mod`). Docker `-f` flag points to the Dockerfile, context is always the root. Dockerfiles that `COPY go.mod` need the root as context.
- **GitHub Actions secrets can't start with `GITHUB_`.** Reserved prefix. Also: app secrets (`POSTGRES_PASSWORD`, `JWT_SECRET`, `ENCRYPTION_KEY`) are runtime secrets — generate strong random values, never reuse `.env` passwords from development.
- **Concurrency control on deploy workflows.** Multiple pushes to main queue multiple deploys. Use `concurrency: { group: deploy, cancel-in-progress: false }` to serialize — new deploys wait for current to finish. Never overlap infrastructure mutations.
- **ARM servers need ARM runners.** `cax11` (Hetzner ARM) produces `linux/arm64` images. GitHub's `ubuntu-latest` is amd64 — can't execute arm64 `RUN` instructions. Use `ubuntu-24.04-arm` runner for native builds, no QEMU.
