# CLAUDE.md — nvoi

## What nvoi is

A CLI that deploys containers to cloud servers. Granular commands hit real infrastructure. `nvoi describe` fetches everything live from the cluster.

## Philosophy

- **`NVOI_APP_NAME` + `NVOI_ENV` is the namespace.** `nvoi-{app}-{env}-*`. Different app or env = brand new infrastructure. No flags. Environment variables.
- **No state files.** No manifest, no database, no local cache. Infrastructure is the source of truth.
- **Everything is idempotent.** Every command hits real infrastructure — provider APIs over HTTP, servers over SSH, cluster via kubectl. Run twice, same result.
- **Naming is the lookup key.** `nvoi-{app}-{env}-{resource}`. Deterministic. No UUIDs. The naming convention finds everything.
- **Everything is `set`.** `instance set`, `volume set`, `dns set`, `service set`, `secret set`, `storage set`. Exists → reconcile. Doesn't exist → create. Same command either way. Always idempotent. Always self-healing. `bin/deploy` runs end to end, every time, same outcome.
- **`describe` fetches everything live from the cluster.** Nodes, workloads, pods, services, ingress, secrets, storage — all via kubectl over SSH.
- **Provider interfaces scale.** Hetzner and Cloudflare first. Interface-first. Add a provider = implement the interface.
- **SSH is the transport.** No agent binary. SSH in, run commands, done.
- **Secrets are k8s secrets.** Values live in the cluster only.
- **Storage credentials are k8s secrets.** `storage set` creates the bucket AND stores S3 credentials in the cluster. `--storage` on `service set` injects them.

## Build & Test

```bash
bin/test                                    # vet + 121 tests across 7 packages
bin/test -v                                 # verbose
bin/test -run TestWaitRollout               # single test
bin/test -cover                             # with coverage
go build ./...                              # build only
```

121 tests in three tiers:
- **Tier 1** — pure functions: naming, YAML generation, Caddyfile, Poll, credential validation, volume parsing, signed URLs, route merging, cloud-init, APIError
- **Tier 2** — mock SSH: WaitRollout terminal errors, kubectl secret ops, Apply, DeleteByName, FirstPod, FindMaster, describe parsers, k3s install, registry, Docker, volume mount/unmount
- **Tier 3** — httptest: Hetzner API (servers, volumes, firewalls, networks, auth), Cloudflare API (buckets, DNS records, credentials)

## CI

Claude Code review runs on GitHub Actions (`.github/workflows/claude-code-review.yml`). Triggers:
- **PR opened** — automatic review on new pull requests
- **Manual** — `workflow_dispatch` from the Actions tab

Does not run on every push/sync to a PR. Manual trigger for re-reviews.

## Local development

Everything runs through Docker Compose. Never run Go on the host — use `bin/cli`.

```bash
bin/cli <command>                       # runs any nvoi command inside compose
bin/deploy                              # full deploy — env vars, zero provider flags
bin/deploy-full                         # full deploy — explicit flags, zero env vars
bin/destroy                             # full teardown — reverse order of deploy
NVOI_ENV=staging bin/deploy             # staging — brand new isolated infra
NVOI_ENV=staging bin/cli instance list  # list staging instances
```

### How it works

`bin/cli` is `docker compose run --rm cli "$@"`. The compose service:

- Mounts source (`.:/app`) — changes picked up instantly, no rebuild
- Mounts SSH keys (`~/.ssh:/root/.ssh:ro`)
- Loads `.env` — provider credentials (`HETZNER_TOKEN`, `CF_API_KEY`, etc.)
- Hardcodes provider selection (`COMPUTE_PROVIDER=hetzner`, `BUILD_PROVIDER=daytona`, `DNS_PROVIDER=cloudflare`, `STORAGE_PROVIDER=cloudflare`)
- Passes `NVOI_APP_NAME` + `NVOI_ENV` from host (defaults: `rails` + `production`)
- Passes `SSH_KEY_PATH=/root/.ssh/id_rsa` — the mounted key
- Caches Go modules across runs (Docker volumes)

Provider selection is hardcoded in `docker-compose.yml`, credentials come from `.env`. This is why `bin/deploy` and `bin/cli` need zero provider flags — compose injects everything.

### First run

```bash
cp .env.example .env                    # fill in provider credentials
bin/cli instance set master --compute-type cx23 --compute-region fsn1
```

### Files

| File | Tracked | Purpose |
|------|---------|---------|
| `.env` | No | Provider credentials (`HETZNER_TOKEN`, `CF_API_KEY`, `CF_ACCOUNT_ID`, `CF_ZONE_ID`, `SSH_KEY_PATH`) |
| `.env.deploy` | No | App secrets for `bin/deploy` (DB creds, Rails master key) |
| `.env.example` | Yes | Template for `.env` |
| `bin/cli` | Yes | `docker compose run --rm cli "$@"` |
| `bin/test` | Yes | `go vet ./... && go test ./... "$@"` |
| `bin/deploy` | Yes | Full deploy — env vars, zero provider flags |
| `bin/deploy-full` | Yes | Full deploy — all flags inline |
| `bin/destroy` | Yes | Full teardown — reverse order of deploy |

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

# ── Infrastructure — instance set installs k3s (master by default, --worker to join)
nvoi instance set <name> --compute-type cx23 --compute-region fsn1
nvoi instance set <name> --compute-type cx33 --compute-region fsn1 --worker
nvoi instance delete <name>
nvoi instance list
nvoi volume set <name> --size 20 --server master
nvoi volume delete <name>
nvoi volume list

# ── DNS + Ingress — creates A record AND deploys Caddy reverse proxy
# "web" is the service name — must have --port set via service set.
# Caddy runs on master with hostNetwork, handles TLS via Let's Encrypt.
nvoi dns set <service> <domain...>                                              # --zone or DNS_ZONE
nvoi dns delete <service> <domain...>
nvoi dns list

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

# Live view — nodes, workloads, pods, services, ingress, secrets, storage
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

# Inspect
nvoi resources

# ── Fully explicit (no env vars) ──────────────────────────────────────────────
nvoi instance set master --compute-provider hetzner --compute-credentials HETZNER_TOKEN=xxx \
  --compute-type cx23 --compute-region fsn1 --app-name rails --environment production
```

See `bin/deploy` for the env-var path (compose injects everything) and `bin/deploy-full` for the full inline flags path.

## Architecture

```
cmd/cli/main.go           CLI entrypoint — signal handling, exit codes

pkg/                       Public library — importable by external repos (API, SDK, etc.)
  app/                     Business logic. One file per domain. No cobra, no I/O, no stdout.
    cluster.go             Cluster struct + ProviderRef type
    output.go              Output interface — the contract between app/ and its viewers
  kube/                    K8s YAML generation + kubectl over SSH + Caddy ingress
  infra/                   SSH, server bootstrap, k3s, Docker, volume mounting, WaitHTTPS
  provider/                ComputeProvider + DNSProvider + BucketProvider + Builder interfaces
    hetzner/               Hetzner Cloud (compute + volumes)
    cloudflare/            Cloudflare (DNS + R2 buckets) — all via core.HTTPClient
    daytona/               Daytona remote builds
    github/                GitHub Actions builds
    local/                 Local docker buildx builds
  core/                    Pure utilities: naming, poll, httpclient (30s default timeout), ssh keys, format
    s3/                    AWS Signature V4 signing for S3-compatible APIs

internal/                  Private — CLI only
  testutil/                MockSSH, MockCompute, MockDNS, MockBucket, MockOutput
  cmd/                     Cobra wrappers. Parse flags → call pkg/app/ → render output.
    resolve.go             Centralized credential resolution — single generic resolveCredentials()
    output_tui.go          TUI renderer (lipgloss-styled terminal output)
    output_json.go         JSONL renderer (structured JSON, one event per line)
    output_plain.go        Plain text renderer (CI/non-TTY environments)
    table.go               Bordered tables with lipgloss (Table + TableGroup for synchronized widths)
```

## Providers

Everything pluggable is a provider. Same pattern for all four kinds: interface + credential schema + `init()` register + factory.

```go
// Registration pattern (same for all provider kinds):
provider.RegisterX("name", CredentialSchema{...}, func(creds map[string]string) XProvider {
    return New(creds)
})
```

| Kind | Flag | Env var | Interface | Implementations |
|------|------|---------|-----------|----------------|
| Compute | `--compute-provider` | `COMPUTE_PROVIDER` | `ComputeProvider` | hetzner |
| DNS | `--dns-provider` | `DNS_PROVIDER` | `DNSProvider` | cloudflare |
| Storage | `--storage-provider` | `STORAGE_PROVIDER` | `BucketProvider` | cloudflare (R2) |
| Build | `--build-provider` | `BUILD_PROVIDER` | `BuildProvider` | local, daytona, github |

`--compute-provider` is on every command that touches infrastructure.
`--build-provider` is only on `build`. It's the only command that needs two providers (compute for registry access + builder for building).

### Provider registration

All providers follow the same architecture:

1. **Interface** — `pkg/provider/{kind}.go` defines the interface
2. **Credential schema** — `pkg/provider/{impl}/register.go` declares required fields with env var mappings
3. **Registration** — `init()` calls `provider.RegisterX(name, schema, factory)`
4. **Blank import** — `internal/cmd/{command}.go` imports `_ "pkg/provider/{impl}"` to trigger `init()`
5. **Resolution** — `provider.ResolveX(name, creds)` validates schema + returns instance

### Credential resolution

All four provider kinds (compute, build, DNS, storage) resolve credentials through a single generic function in `cmd/resolve.go`:

```go
resolveCredentials(cmd, schema, flagName) → map[string]string
```

Resolution order: `--xxx-credentials KEY=VALUE` flag → direct command flag (e.g. `--zone`) → env var from schema. One pattern for all providers. DNS zone is declared in the Cloudflare DNS schema (`EnvVar: "DNS_ZONE"`, `Flag: "zone"`) — no special-casing.

### Credential pairs

Every provider has a name flag + credentials flag. Always a pair. Credentials are `key=value` pairs.

```bash
# Common: env vars set, no credential flags needed
bin/cli instance set master --compute-type cx23 --compute-region fsn1

# Override: --compute-credentials takes priority over env var
bin/cli instance set master \
  --compute-provider hetzner \
  --compute-credentials HETZNER_TOKEN=$OTHER_TOKEN \
  --compute-type cx23 \
  --compute-region fsn1

# Build uses two providers — compute for registry, builder for building
bin/cli build \
  --compute-provider hetzner \
  --compute-credentials HETZNER_TOKEN=xxx \
  --build-provider daytona \
  --build-credentials api_key=xxx \
  --source myorg/app \
  --name web

# Error when missing
# hetzner: token is required (--compute-credentials HETZNER_TOKEN=..., env: HETZNER_TOKEN)
```

### .env

Provider credentials + SSH key. Input, not state.

```
HETZNER_TOKEN=...
CF_API_KEY=...
CF_ACCOUNT_ID=...
CF_ZONE_ID=...
DAYTONA_API_KEY=...
SSH_KEY_PATH=~/.ssh/id_rsa
```

## Apply guardrails

Hard errors before touching k8s.

**Cluster:**
- Server named `master` must exist (resolved from provider API by name `nvoi-{app}-{env}-master`).
- Cluster must have k3s installed (`instance set` handles this — kubectl get nodes succeeds over SSH).

**Services:**
- `service set` takes `--image` only. `--image` is required.
- Build is a separate command. `build` outputs an image ref. `service set` consumes it.

**Build (`nvoi build`):**
- `--compute-provider` = compute provider (for SSH tunnel to cluster registry). Required.
- `--build-provider` = build provider (local, daytona, github). Required.
- `--source` = what to build. Local path (`.`, `./path`) or remote repo (`org/repo`, `https://...`, `git@...`).
- `--name` = image name in the registry. Required.
- `build list` = query registry for all tags. Uses `--compute-provider` only (no builder needed).
- `build latest <name>` = return latest image ref. Pipeable.
- Source + builder validation:
  - Local path (`.` or `/`) + `--build-provider local` → ok.
  - Local path + `--build-provider daytona` → error (Daytona needs a git repo).
  - Remote repo + `--build-provider daytona` → ok.
  - Remote repo + `--build-provider local` → error (local can't clone remote repos).
  - Detection: `--source` starts with `.` or `/` → local. Otherwise → remote.
- The registry IS the state. No build database. `build list` queries the registry directly over SSH.

**Node labeling:**
- `instance set` labels k8s nodes with `nvoi-role={name}` after k3s install/join. Idempotent — runs every deploy.
- This is what `--server` on `service set` matches against (k8s `nodeSelector: {nvoi-role: name}`).

**Placement:**
- `--server` pins a service to a node via k8s node selector matching `nvoi-role`. Defaults to master.
- Services with managed volumes pinned to the volume's server.

**Volumes:**
- Service volume refs must point to volumes that exist (checked via provider API).
- Service with managed volume → StatefulSet, replicas forced to 1.

**DNS / Ingress:**
- `dns set <service> <domain...>` does two things: creates the DNS A record pointing at master, AND deploys Caddy ingress to the cluster.
- Caddy runs as a Deployment with `hostNetwork: true` on the master node (binds port 80/443 directly).
- Caddy reverse-proxies to the k8s Service by name: `domain → service.namespace.svc.cluster.local:port`.
- Caddy handles TLS automatically via Let's Encrypt. No cert management needed.
- Multiple `dns set` calls merge routes into a single Caddy config (ConfigMap). Caddy restarts on config change via annotation checksum.
- `dns delete` removes the A record AND removes the route from Caddy config. If no routes remain, Caddy is deleted entirely.
- `dns set` polls until `https://<domain>` returns 200 (or times out after 2 minutes if TLS is still provisioning).
- Service must have `port > 0`. Hard error if service has no port.

**Storage:**
- `storage set <name>` creates the cloud bucket AND stores S3 credentials as 4 k8s secrets: `STORAGE_{NAME}_ENDPOINT`, `_BUCKET`, `_ACCESS_KEY_ID`, `_SECRET_ACCESS_KEY`.
- Bucket name derived from naming convention: `nvoi-{app}-{env}-{name}`. Override with `--bucket`.
- `--storage <name>` on `service set` expands to the 4 secret refs. Repeatable for multiple buckets.
- `storage empty <name>` deletes all objects (required before bucket deletion on most providers).
- `storage delete <name>` deletes the bucket from the provider AND removes the 4 secrets from the cluster.
- `storage list` discovers configured storages by scanning k8s secrets for `STORAGE_*_BUCKET` keys.

**Secrets:**
- `--secret KEY` on `service set` references a pre-existing secret. Must exist (validated via kubectl before apply). Hard error if not found.
- `--secret KEY=VALUE` is rejected — use `secret set KEY VALUE` first.
- Injected as `env.valueFrom.secretKeyRef` — value never in the manifest.
- `secret delete` is idempotent — no error if key or secret doesn't exist.

**Rollout:**
- `service set` polls pods via `kubectl get pods -o json` with live feedback: `"web: 2/3 ready (ContainerCreating)"`.
- Terminal states exit immediately with error + logs: `CrashLoopBackOff`, `ImagePullBackOff`, `ErrImagePull`, `CreateContainerConfigError`, `OOMKilled`, `Unschedulable`.
- Transient states keep polling: `ContainerCreating`, `PodInitializing`, `Scheduling`.

**Env vars:**
- No rewriting. `POSTGRES_HOST=db` stays `POSTGRES_HOST=db`. K8s namespaces handle isolation — each app+env gets its own namespace, service names stay short.

## Output contract

**Providers are silent. `app/` narrates. `cmd/` renders. No exceptions.**

Strictly enforced across all layers:

| Layer | Writes to stdout? | `fmt.Printf`? | `os.Stdout`? | `"os"` import? |
|-------|-------------------|---------------|-------------|---------------|
| `provider/` | Never | Never | Never | File ops only |
| `app/` | Never | Never | Never | Never |
| `infra/` | Never (writes to `io.Writer` param) | Never | Never | File ops only |
| `kube/` | Never | Never | Never | Never |
| `core/` | Never | Never | Never | Never |
| `cmd/` | Yes — it's the viewer | Yes | Yes | Yes |

### Output interface

`app/` communicates through the `Output` interface on `Cluster`. Six event types:

```go
type Output interface {
    Command(command, action, name string, extra ...any)  // opens a group
    Progress(msg string)                                  // transient status
    Success(msg string)                                   // step completed
    Warning(msg string)                                   // non-fatal issue
    Info(msg string)                                      // informational
    Error(err error)                                      // terminal failure
    Writer() io.Writer                                    // streaming (build logs, SSH, k3s install)
}
```

`cmd/` provides three implementations:

- **TUI** (`output_tui.go`) — lipgloss-styled: bold commands, dimmed progress, green success, yellow warnings, red errors. Default for terminals.
- **JSONL** (`output_json.go`) — one JSON object per line. `--json` flag.
- **Plain** (`output_plain.go`) — aligned tags `[command]` `[progress]` `[success]` etc., no ANSI codes. `--ci` flag or auto-detected in non-TTY.

### JSONL event format

```jsonl
{"type":"command","command":"instance","action":"set","name":"nvoi-rails-production-master","role":"master"}
{"type":"progress","message":"waiting for SSH on 91.98.91.222"}
{"type":"success","message":"SSH ready"}
{"type":"error","message":"SSH not reachable on 91.98.91.222: timeout"}
```

`type:"command"` opens a group. Everything after belongs to it until the next command or error.

### Error handling

- `app/` returns errors. It never renders them. It never calls `Output.Error()`.
- Cobra handles all errors. `root.SetErr()` wires cobra's error output through our `Output.Error()` renderer.
- No `SilenceErrors`. Cobra is the single error path — we style it, not suppress it.
- Single rendering path. No double-printing. No silent swallowing.
- Ctrl+C: `signal.NotifyContext` cancels the context → operations abort → exit 1.

### Streaming

`infra/` functions (k3s install, volume mount) accept `io.Writer` for streaming output. `app/` passes `Output.Writer()`. In TUI mode, lines are dimmed and indented. In JSONL mode, each line becomes a `{"type":"stream","message":"..."}` event. A future API could stream to SSE or websocket through the same interface.

### Cluster struct + ProviderRef

Every `app/` request type embeds `Cluster`:

```go
type Cluster struct {
    AppName, Env, Provider string
    Credentials            map[string]string
    SSHKey                 []byte
    Output                 Output
    SSHFunc                func(ctx, addr) (SSHClient, error)  // nil = real SSH, set in tests
}
```

`Cluster` provides methods: `Names()`, `Compute()`, `Master()`, `SSH()`, `Log()`. Eliminates the 4-line resolve-connect preamble that was duplicated 22 times. `cmd/` constructs `Cluster` with `resolveOutput(cmd)` to wire TUI or JSONL.

Secondary providers (DNS, storage) use `ProviderRef`:

```go
type ProviderRef struct {
    Name  string
    Creds map[string]string
}

// Used on request types:
type DNSSetRequest struct {
    Cluster
    DNS     ProviderRef   // not bare DNSProvider string + DNSCreds map
    Service string
    Domains []string
}
```

### infra.Node

Groups public + private IP for server functions. Eliminates the 5-string parameter explosion in k3s join:

```go
type Node struct {
    PublicIP  string
    PrivateIP string
}

// Before: JoinK3sWorker(ctx, workerIP, workerPrivateIP, masterIP, masterPrivateIP, privKey, w)
// After:  JoinK3sWorker(ctx, worker, master Node, privKey, w)
```

### ServerStatus

Typed enum for server status instead of bare `string`:

```go
type ServerStatus string
const (
    ServerRunning   ServerStatus = "running"
    ServerOff       ServerStatus = "off"
    // ...
)
```

## Key rules

1. `NVOI_APP_NAME` + `NVOI_ENV` (or `--app-name` + `--environment`) are required. They're the namespace for everything.
2. No state files. Infrastructure is the truth. `describe` fetches live from the cluster.
3. Everything is `set`. Idempotent. Run twice, same result. `bin/deploy` is the whole deploy — runs end to end, always same outcome.
4. `set` writes directly to infrastructure. No intermediate files.
5. Provider interfaces scale. Add a provider = implement the interface. Same registration pattern for all four kinds.
6. Naming: `nvoi-{app}-{env}-{resource}`. Deterministic. No UUIDs.
7. SSH is the only transport to remote servers.
8. **`os.Getenv` lives exclusively in `cmd/`.** Environment variables are a CLI concept. `app/`, `provider/`, `infra/`, `core/` never read env vars. All external values (credentials, SSH key path, app name, env) are resolved in `cmd/resolve.go` and passed down as typed function arguments. Strictly enforced. No exceptions.
9. **Providers are silent.** Providers are API clients — they do work and return data. They never print, log, or narrate. Progress output belongs in `app/` via the `Output` interface.
10. **`app/` never writes to stdout.** No `fmt.Printf`, no `os.Stdout`, no `os` import for I/O. All output goes through the `Output` interface. `app/` is a library — a future API handler calls the same functions.
11. **`app/` never imports `net/http`.** HTTP calls belong in `infra/` (e.g. `WaitHTTPS`) or `provider/`. `app/` is pure orchestration.
12. **Errors flow up, render once.** `app/` returns errors. Cobra renders them through `SetErr` → `Output.Error()`. Never double-print. Never swallow silently.
13. Every `delete` command is idempotent. Deleting something that doesn't exist succeeds silently.
14. `bin/destroy` is the reverse of `bin/deploy`. Same commands, `delete` instead of `set`, reverse order. Tolerates missing resources — always runs to completion.
15. **No shell injection.** Secret values flow to kubectl via file upload (`ssh.Upload` + `cat`), not inline `fmt.Sprintf`. `shellQuote` for `--from-literal` args. Never interpolate user values into shell strings.
16. **All providers use `core.HTTPClient`.** 30s default timeout. Consistent `APIError` types. `IsNotFound()` works uniformly. No raw `http.DefaultClient.Do()`.

## Known limitations

- **No pagination on provider list operations.** Hetzner uses `per_page=50` for servers, volumes, firewalls, networks. No cursor continuation. If results exceed one page, the list is silently incomplete — it doesn't error, it lies. Fine at current scale (1-5 servers per app). Fix when adding multi-tenant or `resources` across many apps on one Hetzner account.
- **No retry / backoff on transient HTTP errors.** Provider API calls fail immediately on 500s or connection drops. User re-runs the command. Idempotent `set` design makes this safe. Fix if `bin/deploy` reliability becomes a problem.
- **`s3ops.go` uses a dedicated `s3Client` (not `core.HTTPClient`).** S3/XML operations need raw HTTP, not JSON. Uses `var s3Client = &http.Client{Timeout: 30 * time.Second}` with all `io.ReadAll` errors checked and context propagated. This is by design — `core.HTTPClient` is JSON-oriented.

