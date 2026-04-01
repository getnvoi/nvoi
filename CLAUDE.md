# CLAUDE.md — nvoi

## What nvoi is

A CLI that deploys containers to cloud servers. Granular commands hit real infrastructure. `nvoi show` fetches everything live.

## Philosophy

- **`NVOI_APP_NAME` + `NVOI_ENV` is the namespace.** `nvoi-{app}-{env}-*`. Different app or env = brand new infrastructure. No flags. Environment variables.
- **No state files.** No manifest, no database, no local cache. Infrastructure is the source of truth.
- **Everything is idempotent.** Every command hits real infrastructure — provider APIs over HTTP, servers over SSH, cluster via kubectl. Run twice, same result.
- **Naming is the lookup key.** `nvoi-{app}-{env}-{resource}`. Deterministic. No UUIDs. The naming convention finds everything.
- **Everything is `set`.** `instance set`, `volume set`, `dns set`, `service set`, `secret set`. Exists → reconcile. Doesn't exist → create. Same command either way. Always idempotent. Always self-healing. `bin/deploy` runs end to end, every time, same outcome.
- **`apply` reconciles.** Rebuilds stale images, regenerates ingress, reapplies. One command, full reconciliation.
- **`show` fetches everything live.** Servers from provider API. Pods from kubectl. DNS from DNS API.
- **Provider interfaces scale.** Hetzner and Cloudflare first. Interface-first. Add a provider = implement the interface.
- **SSH is the transport.** No agent binary. SSH in, run commands, done.
- **Secrets are k8s secrets.** Values live in the cluster only.

## Build & Test

```bash
go build ./...
go test ./...
go vet ./...
```

## Local development

Everything runs through Docker Compose. Never run Go on the host — use `bin/cli`.

```bash
bin/cli <command>                       # runs any nvoi command inside compose
bin/deploy                              # full deploy — env vars, zero provider flags
bin/deploy-full                         # full deploy — explicit flags, zero env vars
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
bin/cli instance set master --compute-type cax11 --compute-region fsn1
```

### Files

| File | Tracked | Purpose |
|------|---------|---------|
| `.env` | No | Provider credentials (`HETZNER_TOKEN`, `CF_API_KEY`, `SSH_KEY_PATH`) |
| `.env.deploy` | No | App secrets for `bin/deploy` (DB creds, Rails master key) |
| `.env.example` | Yes | Template for `.env` |
| `bin/cli` | Yes | `docker compose run --rm cli "$@"` |
| `bin/deploy` | Yes | Full deploy — env vars, zero provider flags |
| `bin/deploy-full` | Yes | Full deploy — explicit flags, zero env vars |

## Namespace

Two values. Both required. Everything keys off them. Flag or env var — same result.

```bash
# Via env vars
export NVOI_APP_NAME=dummy-rails
export NVOI_ENV=production
# → nvoi-dummy-rails-production-master, nvoi-dummy-rails-production-fw, ...

# Via flags (overrides env vars)
nvoi instance list --app-name dummy-rails --env staging
# → nvoi-dummy-rails-staging-master, nvoi-dummy-rails-staging-fw, ...
```

Different app or env = completely isolated infrastructure. Same commands, different resources.

## Commands

Every flag has an env var fallback. With env vars set, commands need zero provider flags.

```bash
# ── Flag → env var resolution ──────────────────────────────────────────────────
# --app-name         → NVOI_APP_NAME
# --env              → NVOI_ENV
# --compute-provider → COMPUTE_PROVIDER
# --build-provider   → BUILD_PROVIDER
# --dns-provider     → DNS_PROVIDER
# --storage-provider → STORAGE_PROVIDER
# --compute-credentials KEY=VAL → per-provider env vars (HETZNER_TOKEN, etc.)
# --build-credentials KEY=VAL  → per-provider env vars (DAYTONA_API_KEY, etc.)

# ── Infrastructure — instance set installs k3s (master by default, --worker to join)
nvoi instance set <name> --compute-type cax11 --compute-region fsn1
nvoi instance set <name> --compute-type cax21 --compute-region fsn1 --worker
nvoi instance delete <name>
nvoi instance list
nvoi volume set <name> --size 20 --server master
nvoi volume delete <name>
nvoi volume list
nvoi dns set <service> <domain...> --dns-provider cloudflare --zone nvoi.to
nvoi dns delete <service> <domain...> --dns-provider cloudflare --zone nvoi.to
nvoi dns list --dns-provider cloudflare --zone nvoi.to
nvoi storage set <name> --storage-provider cloudflare --bucket myapp-assets
nvoi storage delete <name>

# Build — separate command, outputs image ref. Registry is the state.
nvoi build --build-provider local --source . --name web
nvoi build --build-provider daytona --source benbonnet/dummy-rails --name web
nvoi build --build-provider github --source benbonnet/dummy-rails --name web
nvoi build --build-provider github --source benbonnet/dummy-rails --name web --architecture arm64
nvoi build list
nvoi build latest <name>                                                        # returns image ref
nvoi build prune <name> --keep 3                                                # keep N, delete rest

# Application — --image only. Build is a separate step.
nvoi service set <name> --image postgres:17 --port 5432
nvoi service set <name> --image $IMAGE --port 3000 --replicas 2
nvoi service delete <name>
nvoi secret set <key> <value>
nvoi secret delete <key>
nvoi secret list

# Reconcile
nvoi apply

# Live view
nvoi show

# Operate
nvoi logs <service> [-f] [-n 50]
nvoi exec <service> -- <command>
nvoi ssh <command>

# Inspect
nvoi resources

# Teardown
nvoi destroy [--yes]

# ── Fully explicit (no env vars) ──────────────────────────────────────────────
nvoi instance set master --compute-provider hetzner --compute-credentials HETZNER_TOKEN=xxx \
  --compute-type cax11 --compute-region fsn1 --app-name rails --env production
```

See `bin/deploy` for the env-var path (compose injects everything) and `bin/deploy-full` for the explicit-flag path (zero env vars).

## Architecture

```
cmd/cli/main.go         CLI entrypoint

internal/
  app/                   Business logic. One file per domain. No cobra, no I/O formatting.
                         Called by cmd/ (CLI) and future API handlers.
  cmd/                   Thin cobra wrappers. Parse flags → call app/ → format output.
  kube/                  K8s YAML generation + kubectl over SSH
  infra/                 SSH, server bootstrap, k3s, Docker, volume mounting
  provider/              ComputeProvider + DNSProvider + BucketProvider + Builder interfaces
    hetzner/             Hetzner Cloud (compute + volumes)
    cloudflare/          Cloudflare (DNS + R2 buckets)
    daytona/             Daytona remote builds
  core/                  Pure utilities: naming, poll, httpclient, ssh keys
```

## Providers

Everything pluggable is a provider. Same pattern: interface + credential schema + register.

| Kind | Flag | Env var | Credentials flag | Interface | Implementations |
|------|------|---------|-----------------|-----------|----------------|
| Compute | `--compute-provider` | `COMPUTE_PROVIDER` | `--compute-credentials` | `ComputeProvider` | hetzner, scaleway (future) |
| DNS | `--dns-provider` | `DNS_PROVIDER` | — | `DNSProvider` | cloudflare, hetzner (future) |
| Storage | `--storage-provider` | `STORAGE_PROVIDER` | — | `BucketProvider` | cloudflare, aws (future) |
| Build | `--build-provider` | `BUILD_PROVIDER` | `--build-credentials` | `BuildProvider` | local, daytona, github |

`--compute-provider` is on every command that touches infrastructure.
`--build-provider` is only on `build`. It's the only command that needs two providers (compute for registry access + builder for building).

### Credential pairs

Every provider has a name flag + credentials flag. Always a pair. Credentials are `key=value` pairs.

```bash
# Common: env vars set, no credential flags needed
bin/cli instance set master --compute-type cax11 --compute-region fsn1

# Override: --compute-credentials takes priority over env var
bin/cli instance set master \
  --compute-provider hetzner \
  --compute-credentials HETZNER_TOKEN=$OTHER_TOKEN \
  --compute-type cax11 \
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

**Placement:**
- `--server` pins a service to a node via k8s node selector. Defaults to master.
- Services with managed volumes pinned to the volume's server.

**Volumes:**
- Service volume refs must point to volumes that exist (checked via provider API).
- Service with managed volume → StatefulSet, replicas forced to 1.

**DNS / Ingress:**
- If DNS records exist, Caddy ingress auto-generated. Service must have `port > 0`.

**Secrets:**
- If secrets exist, injected as `envFrom: secretRef` into service pods.

**Env vars:**
- No rewriting. `POSTGRES_HOST=db` stays `POSTGRES_HOST=db`. K8s namespaces handle isolation — each app+env gets its own namespace, service names stay short.

## Key rules

1. `NVOI_APP_NAME` + `NVOI_ENV` (or `--app-name` + `--env`) are required. They're the namespace for everything.
2. No state files. Infrastructure is the truth. `show` fetches live.
3. Everything is `set`. Idempotent. Run twice, same result. `bin/deploy` is the whole deploy — runs end to end, always same outcome.
4. `set` writes directly to infrastructure. No intermediate files.
5. `apply` reconciles everything — services, ingress, secrets.
6. Provider interfaces scale. Add a provider = implement the interface.
7. Naming: `nvoi-{app}-{env}-{resource}`. Deterministic. No UUIDs.
8. SSH is the only transport to remote servers.
9. **`os.Getenv` lives exclusively in `cmd/`.** Environment variables are a CLI concept. `app/`, `provider/`, `infra/`, `core/` never read env vars. All external values (credentials, SSH key path, app name, env) are resolved in `cmd/resolve.go` and passed down as typed function arguments. Strictly enforced. No exceptions.
