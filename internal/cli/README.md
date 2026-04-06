# nvoi CLI

## Quick start

```bash
# Authenticate
nvoi login

# Create a workspace and repo
nvoi workspaces create my-team
nvoi workspaces use my-team
nvoi repos create my-rails-app
nvoi repos use my-rails-app

# Push config + env
nvoi push --config nvoi.yaml --env .env --compute-provider hetzner --dns-provider cloudflare

# Preview the plan
nvoi plan

# Deploy
nvoi deploy
```

## Authentication

```bash
# Login — tries gh CLI token, then GITHUB_TOKEN env, then prompts
nvoi login

# Check current context
nvoi whoami
```

`nvoi login` saves a JWT to `~/.config/nvoi/auth.json`. All subsequent commands use it.

## Workspaces

```bash
# List workspaces (* = active)
nvoi workspaces list

# Create
nvoi workspaces create production

# Switch active workspace
nvoi workspaces use production

# Delete
nvoi workspaces delete <workspace-id>
```

Alias: `nvoi ws list`

## Repos

```bash
# List repos in active workspace (* = active)
nvoi repos list

# Create
nvoi repos create my-rails-app

# Switch active repo
nvoi repos use my-rails-app

# Delete
nvoi repos delete <repo-id>
```

## Push config

```bash
# Push config YAML + .env to the active repo
nvoi push --config nvoi.yaml --env .env \
  --compute-provider hetzner \
  --dns-provider cloudflare \
  --storage-provider cloudflare \
  --build-provider daytona
```

The config describes what to deploy. The .env provides provider credentials and app secrets. Each push creates a new versioned snapshot.

Example `nvoi.yaml`:

```yaml
servers:
  master:
    type: cx23
    region: fsn1
  worker-1:
    type: cx33
    region: fsn1

volumes:
  pgdata:
    size: 30
    server: master

build:
  web:
    source: benbonnet/dummy-rails

storage:
  assets:
    cors: true

services:
  db:
    managed: postgres
  web:
    build: web
    port: 80
    replicas: 2
    health: /up
    server: worker-1
    env:
      - RAILS_ENV=production
      - POSTGRES_HOST=db
      - POSTGRES_USER
      - POSTGRES_DB
    secrets:
      - RAILS_MASTER_KEY
    storage:
      - assets
    uses:
      - db
  jobs:
    build: web
    command: bin/jobs
    server: worker-1
    uses: [db]

domains:
  web: app.example.com
```

## Plan

```bash
# Show the execution plan for the latest config
nvoi plan
```

Output:

```
plan for config v3 (12 steps):

   1. instance.set master
   2. instance.set worker-1
   3. volume.set pgdata
   4. build web
   5. secret.set POSTGRES_PASSWORD_DB
   6. secret.set RAILS_MASTER_KEY
   7. storage.set assets
   8. service.set db
   9. service.set web
  10. service.set jobs
  11. dns.set web
```

## Deploy

```bash
# Deploy the latest config — streams live output
nvoi deploy
```

Output is identical to running `examples/core/hetzner/deploy` directly — same lipgloss TUI formatting, same events. The deploy triggers on the API, logs stream back as JSONL, and the CLI renders them through the shared TUI renderer.

## Logs

```bash
# Stream logs for a specific deployment
nvoi logs <deployment-id>
```

Renders the same JSONL events through the TUI — command groups, progress, success/failure, streaming output.

## Describe

```bash
# Live cluster state — nodes, workloads, pods, services, ingress, secrets, storage
nvoi describe
```

## Resources

```bash
# List all provider resources
nvoi resources
```
