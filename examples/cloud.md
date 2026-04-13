# Deploy via nvoi cloud

Full workflow to deploy an app using the cloud CLI. The cloud CLI sends your config to the nvoi API, which runs the reconcile loop server-side and streams progress back.

## Prerequisites

- GitHub account (authentication uses GitHub tokens)
- Provider credentials as env vars (or pass them inline)

## 1. Login

```bash
nvoi login
```

Authenticates via GitHub token. Resolves from `gh auth token`, then `GITHUB_TOKEN` env var, then interactive prompt. Stores a JWT in `~/.config/nvoi/auth.json`.

## 2. Create a repo

```bash
nvoi repos create myapp
```

A repo is your project. It holds provider links, SSH keys, and command history. Idempotent — if `myapp` already exists, it selects it.

## 3. Register providers

Pass credentials inline as `KEY=VALUE` args, or let them resolve from env vars automatically.

Each provider gets a user-chosen alias (`--name`) used for linking to repos. If omitted, defaults to the provider name.

```bash
# Compute — where your servers run
nvoi provider add hetzner --kind compute --name hetzner-prod HETZNER_TOKEN=hcloud_xxx

# DNS — where your domains point
nvoi provider add cloudflare --kind dns --name cf-dns CF_API_KEY=xxx CF_ZONE_ID=xxx DNS_ZONE=myapp.com

# Storage — object storage for backups, assets
nvoi provider add cloudflare --kind storage --name cf-storage CF_ACCOUNT_ID=xxx CF_R2_ACCESS_KEY_ID=xxx CF_R2_SECRET_ACCESS_KEY=xxx

# Build — where container images are built (daytona or github, not local)
nvoi provider add daytona --kind build --name daytona-team DAYTONA_API_KEY=xxx
```

You can register multiple providers of the same type with different aliases:

```bash
nvoi provider add hetzner --kind compute --name hetzner-prod HETZNER_TOKEN=prod_token
nvoi provider add hetzner --kind compute --name hetzner-staging HETZNER_TOKEN=staging_token
```

If a key is already in your environment, you can omit it — env vars are the fallback:

```bash
export HETZNER_TOKEN=hcloud_xxx
nvoi provider add hetzner --kind compute   # picks up HETZNER_TOKEN from env, alias defaults to "hetzner"
```

Other supported providers:

| Kind | Providers |
|------|-----------|
| compute | hetzner, aws, scaleway |
| dns | cloudflare, aws, scaleway |
| storage | cloudflare, aws, scaleway |
| build | local*, daytona, github |

\* `local` is only available with the direct CLI (`bin/nvoi deploy`). Cloud deployments require `daytona` or `github`.

## 4. Link providers to the repo

```bash
nvoi repos use myapp \
  --compute hetzner-prod \
  --dns cf-dns \
  --storage cf-storage \
  --build daytona-team
```

The flags take provider aliases — the `--name` you chose in step 3. This tells the API which credentials to use when running commands for this repo.

## 5. Write your config

```yaml
# nvoi.yaml
app: myapp
env: production

providers:
  compute: hetzner
  dns: cloudflare
  storage: cloudflare
  build: daytona

servers:
  master:
    type: cax11
    region: nbg1
    role: master

firewall: default

volumes:
  pgdata:
    size: 20
    server: master

database:
  main:
    image: postgres:17
    volume: pgdata

secrets:
  - JWT_SECRET

storage:
  uploads: {}

build:
  api: ./cmd/api
  web: ./cmd/web

services:
  api:
    build: api
    port: 8080
    secrets: [JWT_SECRET]
  web:
    build: web
    port: 3000

domains:
  web: [myapp.com, www.myapp.com]
  api: [api.myapp.com]
```

## 6. Deploy

```bash
nvoi deploy
```

Sends `nvoi.yaml` to the API. The API runs the full reconcile loop and streams JSONL progress back. The CLI renders it as a live TUI.

Deploy is idempotent. Run it twice, same result. It reconciles: adds what's missing, removes what's orphaned.

## 7. Inspect

```bash
# Live cluster state — pods, services, nodes, volumes, DNS, storage
nvoi describe

# All provider resources
nvoi resources
```

## 8. Operate

```bash
# Stream logs
nvoi logs api
nvoi logs web -f

# Shell into a service pod
nvoi exec api -- sh

# Run a command on the master node
nvoi ssh -- kubectl get pods

# Trigger a cron job
nvoi cron run db-backup
```

## 9. Teardown

```bash
nvoi teardown
```

Hard-nukes all provider resources (servers, firewalls, networks, DNS records). Volumes and storage buckets are preserved by default.

```bash
# Nuke everything including data
nvoi teardown --delete-volumes --delete-storage
```

## Local development

`bin/cloud` auto-starts a local API + postgres via docker-compose, then runs the cloud CLI against `localhost:8080`:

```bash
bin/cloud login
bin/cloud repos create myapp
bin/cloud provider set compute hetzner
bin/cloud deploy
```
