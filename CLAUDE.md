# CLAUDE.md — nvoi

## What nvoi is

A CLI that deploys containers to cloud servers. Granular commands hit real infrastructure. `nvoi show` fetches everything live.

## Philosophy

- **`NVOI_APP_NAME` + `NVOI_ENV` is the namespace.** `nvoi-{app}-{env}-*`. Different app or env = brand new infrastructure. No flags. Environment variables.
- **No state files.** No manifest, no database, no local cache. Infrastructure is the source of truth.
- **Everything is idempotent.** Every command hits real infrastructure — provider APIs over HTTP, servers over SSH, cluster via kubectl. Run twice, same result.
- **Naming is the lookup key.** `nvoi-{app}-{env}-{resource}`. Deterministic. No UUIDs. The naming convention finds everything.
- **Everything is `set`.** `compute set`, `volume set`, `dns set`, `service set`, `secret set`. Exists → reconcile. Doesn't exist → create. Same command either way. Always idempotent. Always self-healing. `bin/deploy` runs end to end, every time, same outcome.
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
bin/deploy                              # full deploy — idempotent, self-healing
NVOI_ENV=staging bin/deploy             # staging — brand new isolated infra
NVOI_ENV=staging bin/cli compute list   # list staging servers
```

### How it works

`bin/cli` is `docker compose run --rm cli "$@"`. The compose service:

- Mounts source (`.:/app`) — changes picked up instantly, no rebuild
- Mounts SSH keys (`~/.ssh:/root/.ssh:ro`)
- Loads `.env` — provider credentials (`HETZNER_TOKEN`, `CF_API_KEY`, etc.)
- Passes `NVOI_APP_NAME` + `NVOI_ENV` from host (defaults: `rails` + `production`)
- Passes `SSH_KEY_PATH=/root/.ssh/id_rsa` — the mounted key
- Caches Go modules across runs (Docker volumes)

### First run

```bash
cp .env.example .env                    # fill in provider credentials
bin/cli compute set master --provider hetzner --type cax11 --region fsn1
```

### Files

| File | Tracked | Purpose |
|------|---------|---------|
| `.env` | No | Provider credentials (`HETZNER_TOKEN`, `CF_API_KEY`, `SSH_KEY_PATH`) |
| `.env.deploy` | No | App secrets for `bin/deploy` (DB creds, Rails master key) |
| `.env.example` | Yes | Template for `.env` |
| `bin/cli` | Yes | `docker compose run --rm cli "$@"` |
| `bin/deploy` | Yes | Full deploy script — sources `.env.deploy`, runs all commands |

## Namespace

Two environment variables. Both required. Everything keys off them.

```bash
export NVOI_APP_NAME=dummy-rails
export NVOI_ENV=production
# → nvoi-dummy-rails-production-master, nvoi-dummy-rails-production-fw, ...

export NVOI_ENV=staging
# → nvoi-dummy-rails-staging-master, nvoi-dummy-rails-staging-fw, ...
```

Different app or env = completely isolated infrastructure. Same commands, different resources.

## Commands

```bash
# Infrastructure
nvoi compute set <name> --provider hetzner --type cax11 --region nbg1
nvoi compute delete <name>
nvoi compute list
nvoi bootstrap
nvoi volume set <name> --size 20 --server master
nvoi volume delete <name>
nvoi volume list
nvoi dns set <service> <domain...> --provider cloudflare --zone nvoi.to
nvoi dns delete <service> <domain...> --provider cloudflare --zone nvoi.to
nvoi dns list --provider cloudflare --zone nvoi.to
nvoi storage set <name> --provider cloudflare --bucket myapp-assets
nvoi storage delete <name>

# Application
nvoi service set <name> --image postgres:17 --port 5432
nvoi service set <name> --build myorg/myapp --branch main --port 3000 --replicas 2
nvoi service delete <name>
nvoi secret set <key> <value>
nvoi secret delete <key>
nvoi secret list

# Build
nvoi build [repo] [--branch main]

# Reconcile
nvoi apply

# Live view
nvoi show

# Operate
nvoi logs <service> [-f] [-n 50]
nvoi exec <service> -- <command>
nvoi ssh <command>

# Teardown
nvoi destroy [--yes]
```

## Architecture

```
cmd/cli/main.go         CLI entrypoint

internal/
  cmd/                   One file per command. Thin: parse args, call infra/kube.
  kube/                  K8s YAML generation + kubectl over SSH
  infra/                 SSH, server bootstrap, k3s, volume mounting
  provider/              ComputeProvider + DNSProvider + BucketProvider + Builder interfaces
    hetzner/             Hetzner Cloud (compute + volumes)
    cloudflare/          Cloudflare (DNS + R2 buckets)
    daytona/             Daytona remote builds
  core/                  Pure utilities: naming, poll, httpclient, ssh keys
```

## .env

Provider credentials. Input, not state.

```
HETZNER_TOKEN=...
CF_API_KEY=...
CF_ZONE_ID=...
SSH_KEY_PATH=~/.ssh/id_ed25519
```

## Apply guardrails

Hard errors before touching k8s.

**Cluster:**
- Server named `master` must exist (resolved from provider API by name `nvoi-{app}-{env}-master`).
- Cluster must be bootstrapped (kubectl get nodes succeeds over SSH).

**Services:**
- Every service must have `image` or `build`. Neither = hard error.
- If `build` set but `image` missing/stale, `apply` builds automatically.

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

1. `NVOI_APP_NAME` + `NVOI_ENV` are required. They're the namespace for everything.
2. No state files. Infrastructure is the truth. `show` fetches live.
3. Everything is `set`. Idempotent. Run twice, same result. `bin/deploy` is the whole deploy — runs end to end, always same outcome.
4. `set` writes directly to infrastructure. No intermediate files.
5. `apply` reconciles everything — services, ingress, secrets.
6. Provider interfaces scale. Add a provider = implement the interface.
7. Naming: `nvoi-{app}-{env}-{resource}`. Deterministic. No UUIDs.
8. SSH is the only transport to remote servers.
