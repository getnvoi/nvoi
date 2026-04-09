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
- **Provider interfaces scale.** Hetzner, Cloudflare, AWS, Scaleway. Interface-first. Add a provider = implement the interface.
- **SSH is the transport.** No agent binary. SSH in, run commands, done.
- **Secrets are k8s secrets.** Values live in the cluster only.
- **Storage credentials are k8s secrets.** `storage set` creates the bucket AND stores S3 credentials in the cluster. `--storage` on `service set` injects them.

## Build & Test

```bash
go test ./...
go test ./... -v
go test ./... -run TestWaitRollout
go test ./... -cover
go build ./cmd/core
```

Tests in three tiers:
- **Tier 1** — pure functions: naming, YAML generation, Caddyfile, Poll, credential validation, volume parsing, signed URLs, route merging, cloud-init (hostname), APIError, AWS ArchForType, instanceFromEC2, volumeFromEC2, nvoiTags, deref helpers
- **Tier 2** — mock SSH: WaitRollout terminal errors, kubectl secret ops, Apply, DeleteByName, FirstPod, FindMaster, describe parsers, k3s install, registry, Docker, volume mount/unmount
- **Tier 3** — httptest: Hetzner API (servers, volumes, firewalls, networks, auth), Cloudflare API (buckets, DNS records, credentials), AWS provider resolution (compute, DNS, missing creds)

## CI

Claude Code review runs on GitHub Actions (`.github/workflows/claude-code-review.yml`). Triggers:
- **PR opened** — automatic review on new pull requests
- **Manual** — `workflow_dispatch` from the Actions tab

**PR merges:** Never squash. Use `gh pr merge --merge --delete-branch`. Each commit is a meaningful change — preserve the history.

## Local development

```bash
bin/core instance list                           # direct CLI (go run ./cmd/core)
bin/core describe                                # live cluster state
bin/cloud login                                  # cloud CLI (go run ./cmd/cli)
go test ./...                                    # run tests
```

### First run

```bash
cp examples/.env.example examples/.env  # fill in credentials
./examples/deploy hetzner               # deploy to any provider
```

### Files

| File | Purpose |
|------|---------|
| `.env` | Real app deploy env only (not tracked) |
| `.env.example` | Template for real app deploy env |
| `examples/.env` | Example/dev env only (not tracked) |
| `examples/.env.example` | Template for example/dev env |
| `examples/deploy` | Generic deploy script — reads provider JSON |
| `examples/destroy` | Generic destroy script — reverse order |
| `examples/providers/*.json` | Provider-specific config (compute type, region, domain) |
| `bin/core` | Direct CLI — `go run ./cmd/core` |
| `bin/cloud` | Cloud CLI — `go run ./cmd/cli` |
| `bin/dev` | Website development loop |
| `bin/nvoi` | Cached `cmd/core` binary wrapper for `bin/deploy` and `bin/destroy` |
| `bin/deploy` | Real app deploy entrypoint |
| `bin/destroy` | Real app destroy entrypoint |

See [`examples/README.md`](examples/README.md) for deploy workflows.

## Namespace

Two values. Both required. Everything keys off them. Flag or env var — same result.

```bash
# Via env vars
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

# ── Infrastructure
nvoi instance set <name> --compute-type cx23 --compute-region fsn1 --role master
nvoi instance set <name> --compute-type cx33 --compute-region fsn1 --role worker
nvoi instance delete <name>
nvoi instance list
nvoi volume set <name> --size 20 --server master
nvoi volume delete <name>
nvoi volume list

# ── DNS
nvoi dns set <service:domain,domain ...>
nvoi dns delete <service:domain,domain ...>
nvoi dns list

# ── Ingress — Caddy routes/TLS (ACME only)
nvoi ingress set <service:domain,domain ...>
nvoi ingress delete <service:domain,domain ...>

# ── Storage
nvoi storage set <name>
nvoi storage set <name> --cors
nvoi storage set <name> --expire-days 30
nvoi storage set <name> --bucket custom-name
nvoi storage list
nvoi storage empty <name>
nvoi storage delete <name>

# ── Build
nvoi build --target web:benbonnet/dummy-rails
nvoi build list
nvoi build latest <name>
nvoi build prune <name> --keep 3

# ── Secrets
nvoi secret set <key> <value>
nvoi secret delete <key>
nvoi secret list
nvoi secret reveal <key>

# ── Services
nvoi service set <name> --image postgres:17 --port 5432 --secret DB_PASSWORD
nvoi service set <name> --image $IMAGE --port 3000 --replicas 2 --secret RAILS_MASTER_KEY --storage assets
nvoi service delete <name>

# ── Cron
nvoi cron set <name> --image busybox --schedule "0 1 * * *" --command "echo hello"
nvoi cron delete <name>

# ── Live view
nvoi describe

# ── Operate
nvoi logs <service>
nvoi logs <service> -f
nvoi exec <service> -- <command>
nvoi ssh <command>

# ── Inspect
nvoi resources
nvoi resources --json
```

## Architecture

```
cmd/
  core/main.go             Direct CLI entrypoint
  cli/main.go              Cloud CLI entrypoint
  api/main.go              API server entrypoint

pkg/                       Public library — the execution engine
  core/                    Business logic. One file per domain. No cobra, no I/O, no stdout.
    cluster.go             Cluster struct + ProviderRef type
    output.go              Output interface — the contract between core/ and its viewers
    compute.go             ComputeSet, ComputeDelete, ComputeList
    service.go             ServiceSet, ServiceDelete
    dns.go                 DNSSet, DNSDelete, DNSList
    ingress.go             IngressSet, IngressDelete
    storage.go             StorageSet, StorageDelete, StorageEmpty, StorageList
    secret.go              SecretSet, SecretDelete, SecretList, SecretReveal
    volume.go              VolumeSet, VolumeDelete, VolumeList
    build.go               BuildRun, BuildList, BuildLatest, BuildPrune
    cron.go                CronSet, CronDelete
    describe.go            Describe — live cluster state
    resources.go           Resources — all provider resources
    firewall.go            FirewallSet
    wait.go                WaitRollout — polls pods with terminal failure detection
  kube/                    K8s YAML generation + kubectl over SSH + Caddy ingress
  infra/                   SSH, server bootstrap, k3s, Docker, volume mounting
  provider/                ComputeProvider + DNSProvider + BucketProvider + Builder interfaces
    hetzner/               Hetzner Cloud (compute + volumes)
    cloudflare/            Cloudflare (DNS + R2 buckets)
    aws/                   AWS (EC2 + VPC + Route53 + S3)
    scaleway/              Scaleway (compute + DNS)
    daytona/               Daytona remote builds
    github/                GitHub Actions builds
    local/                 Local docker buildx builds
  utils/                   Pure utilities: naming, poll, httpclient, ssh keys, format, maps, params

internal/                  Private
  commands/                Shared command tree — cobra commands, Backend interface
    backend.go             Backend interface (24 methods), WorkloadOpts, ServiceOpts, etc.
    instance.go            instance set/delete/list
    service.go             service set/delete
    ...                    One file per command group
  core/                    DirectBackend — implements Backend, calls pkg/core/ directly
    backend.go             DirectBackend struct
    root.go                Root cobra command, provider/credential resolution
    resolve.go             Flag → env var resolution
  cli/                     CloudBackend — implements Backend, calls API via HTTP
    backend.go             CloudBackend struct, run() helper, all Backend methods
    root.go                Root cobra command
    client.go              HTTP client (APIClient, doRaw, parseAPIError)
    auth.go                Auth config persistence (~/.config/nvoi/auth.json)
    login.go               GitHub token → JWT flow
    provider.go            nvoi provider set/list/delete
    repos.go               nvoi repos create/list/use/delete
    workspaces.go          nvoi workspaces create/list/use/delete
    whoami.go              nvoi whoami
  api/                     REST API server
    models.go              User, Workspace, Repo, InfraProvider, CommandLog
    handlers/run.go        POST /run — single dispatch endpoint, streams JSONL
    handlers/query.go      Read-only endpoints (instances, volumes, dns, etc.)
    handlers/describe.go   Describe + Resources + clusterFromRepo helper
    handlers/router.go     All route registration
  render/                  Shared renderers — TUI, Plain, JSON, Table, ReplayLine
  testutil/                MockSSH, MockCompute, MockDNS, MockBucket, MockOutput
```

### Two CLIs, one Backend interface

`internal/commands/` defines the shared command tree and the `Backend` interface (24 methods). Both CLIs register the same cobra commands with different Backend implementations:

- **DirectBackend** (`internal/core/`) — resolves providers from flags/env, calls `pkg/core/` directly over SSH
- **CloudBackend** (`internal/cli/`) — calls the API's `/run` endpoint, streams JSONL back through the TUI

Both produce identical output. Same lipgloss, same events, same look.

### Cloud CLI flow

```
CloudBackend.InstanceSet(name, type, region, role)
  → c.run("instance.set", name, {type, region, role})
    → POST /repos/:rid/run {kind, name, params}
      → API loads repo + InfraProvider credentials
      → dispatch() calls pkg/core.ComputeSet()
      → streams JSONL output back
      → logs CommandLog row
    → CLI renders JSONL through TUI
```

## Providers

Everything pluggable is a provider. Same pattern for all four kinds: interface + credential schema + `init()` register + factory.

| Kind | Flag | Env var | Interface | Implementations |
|------|------|---------|-----------|----------------|
| Compute | `--compute-provider` | `COMPUTE_PROVIDER` | `ComputeProvider` | hetzner, aws, scaleway |
| DNS | `--dns-provider` | `DNS_PROVIDER` | `DNSProvider` | cloudflare, aws, scaleway |
| Storage | `--storage-provider` | `STORAGE_PROVIDER` | `BucketProvider` | cloudflare (R2), aws (S3) |
| Build | `--build-provider` | `BUILD_PROVIDER` | `BuildProvider` | local, daytona, github |

See [`pkg/provider/CLAUDE.md`](pkg/provider/CLAUDE.md) for registration pattern, credential resolution, credential pairs, and `.env` reference.

## Apply guardrails

Hard errors before touching k8s.

- **Cluster:** master must exist. k3s must be installed.
- **Services:** `--image` required. Build is separate (`build` outputs image ref, `service set` consumes it).
- **Cron:** `--image` and `--schedule` required. Reuses service conventions (secrets, storage, volumes, node selector).
- **Build:** `--compute-provider` + `--build-provider` + `--source` + `--name` required. Local path + remote builder = error. Remote repo + local builder = error. Registry is the state.
- **Node labeling:** `instance set` labels nodes `nvoi-role={name}`. `--server` on `service set` matches via `nodeSelector`.
- **Placement:** `--server` pins to node. Volume services pinned to volume's server.
- **Volumes:** refs must exist (checked via provider API). Volume → StatefulSet, replicas=1. `volume delete` unmounts then deletes.
- **DNS / Ingress:** `dns set` creates A records. `ingress set` deploys Caddy with `hostNetwork`. TLS is ACME only. Service must have `port > 0`.
- **Storage:** `storage set` creates bucket + stores S3 creds as 4 k8s secrets. `--storage` on `service set` expands to secret refs.
- **Secrets:** `--secret KEY` references pre-existing secret (hard error if missing). `--secret KEY=VALUE` is rejected — use `secret set` first. `secret delete` is idempotent.
- **Rollout:** polls pods with live feedback. Terminal states (`CrashLoopBackOff`, `ImagePullBackOff`, `OOMKilled`) exit immediately with logs.

## Output contract

**Providers are silent. `pkg/core/` narrates. `internal/core/` renders. No exceptions.**

- `pkg/core/` communicates through the `Output` interface (Command, Progress, Success, Warning, Info, Error, Writer)
- `pkg/core/` returns errors. Never renders them. Never calls `Output.Error()`.
- Cobra handles all errors via `root.SetErr()` → `Output.Error()`. Single path.
- Three renderers: TUI (terminal), JSONL (`--json`), Plain (`--ci` or non-TTY)
- Both CLIs produce identical output. Direct CLI calls `pkg/core/` → `Output`. Cloud CLI replays JSONL from the API through the same renderers.

See [`internal/render/CLAUDE.md`](internal/render/CLAUDE.md) for renderer details, JSONL format, streaming.

## Key rules

1. `NVOI_APP_NAME` + `NVOI_ENV` (or `--app-name` + `--environment`) are required. They're the namespace for everything.
2. No state files. Infrastructure is the truth. `describe` fetches live from the cluster.
3. Everything is `set`. Idempotent. Run twice, same result. Deploy scripts run end to end, always same outcome.
4. `set` writes directly to infrastructure. No intermediate files.
5. Provider interfaces scale. Add a provider = implement the interface. Same registration pattern for all four kinds.
6. Naming: `nvoi-{app}-{env}-{resource}`. Deterministic. No UUIDs.
7. SSH is the only transport to remote servers. SSH keys are injected strictly via cloud-init UserData — never via provider SSH key APIs (e.g. Hetzner `ssh_keys`, AWS `KeyName`). `infra.RenderCloudInit` renders the public key into `ssh_authorized_keys`. This is the only key injection path.
8. **`os.Getenv` lives exclusively in `internal/core/`.** Environment variables are a CLI concept. `pkg/core/`, `provider/`, `infra/`, `utils/` never read env vars. All external values are resolved in `internal/core/resolve.go` and passed down as typed function arguments. Strictly enforced. No exceptions.
9. **Providers are silent.** Providers are API clients — they do work and return data. They never print, log, or narrate. Progress output belongs in `pkg/core/` via the `Output` interface.
10. **`pkg/core/` never writes to stdout.** No `fmt.Printf`, no `os.Stdout`, no `os` import for I/O. All output goes through the `Output` interface. `pkg/core/` is a library — the API handlers call the same functions.
11. **`pkg/core/` never imports `net/http`.** HTTP calls belong in `infra/` or `provider/`. `pkg/core/` is pure orchestration.
12. **Errors flow up, render once.** `pkg/core/` returns errors. Cobra renders them through `SetErr` → `Output.Error()`. Never double-print. Never swallow silently.
13. Every `delete` command is idempotent. Deleting something that doesn't exist succeeds silently. Typed sentinel errors drive the rendering: `utils.ErrNotFound` → "already gone", `core.ErrNoMaster` → "cluster gone". `internal/render/delete.go` `HandleDeleteResult()` dispatches these.
14. `examples/destroy` is the reverse of `examples/deploy`. Same commands, `delete` instead of `set`, reverse order. Tolerates missing resources — always runs to completion.
15. **No shell injection.** Secret values flow to kubectl via file upload (`ssh.Upload` + `cat`), not inline `fmt.Sprintf`. `shellQuote` for `--from-literal` args. Never interpolate user values into shell strings.
16. **All providers use `utils.HTTPClient`.** 30s default timeout. Consistent `APIError` types. `IsNotFound()` works uniformly. No raw `http.DefaultClient.Do()`. Exception: AWS provider uses AWS SDK v2 (its own HTTP transport).

## Known limitations

- **No pagination on provider list operations.** Hetzner uses `per_page=50` for servers, volumes, firewalls, networks. No cursor continuation. Fine at current scale. Fix when adding multi-tenant.
- **No retry / backoff on transient HTTP errors.** Provider API calls fail immediately on 500s or connection drops. User re-runs the command. Idempotent `set` design makes this safe.
- **`s3ops.go` uses a dedicated `s3Client` (not `utils.HTTPClient`).** S3/XML operations need raw HTTP, not JSON. By design.
- **AWS SDK `LoadDefaultConfig` errors are deferred to `ValidateCredentials`.** Provider factories can't return errors. AWS constructors store the config error and surface it on the first `ValidateCredentials` call.

## Production hardening notes

Lessons from real deployment failures.

- **`~` doesn't expand in Go.** `resolveSSHKey()` calls `expandHome()` before reading. Any path from env vars or flags that could contain `~` must expand it.
- **`kubectl apply` does strategic merge, not full replace.** `kube.Apply()` uses `kubectl replace` first, falls back to `kubectl apply --server-side --force-conflicts` for first creation.
- **Caddy with `hostNetwork` can't rolling-update on single-node.** Caddy Deployment uses `Recreate` strategy.
- **DNS and ingress are separate concerns.** `dns set` creates A records only. `ingress set` owns Caddy entirely — takes all `service:domain` mappings, builds full Caddyfile, deploys once.
- **`WaitAllServices` must detect terminal failures.** `CrashLoopBackOff` and `Error` statuses trigger early exit after `waitCrashTimeout` (2 min). On bail-out, fetch `--previous` logs.
- **`service set` in deploy scripts needs `--no-wait`.** Each `service set` calls `WaitAllServices` which checks ALL pods in the namespace. Use `--no-wait` on all but the last service.
- **Build source and Dockerfile are separate.** `--source ./cmd/web` means the Dockerfile lives at `./cmd/web/Dockerfile`. Build context is the project root.
- **GitHub Actions secrets can't start with `GITHUB_`.** Reserved prefix.
- **Concurrency control on deploy workflows.** Use `concurrency: { group: deploy, cancel-in-progress: false }` to serialize.
- **ARM servers need ARM runners.** `cax11` (Hetzner ARM) produces `linux/arm64` images. Use `ubuntu-24.04-arm` runner.
