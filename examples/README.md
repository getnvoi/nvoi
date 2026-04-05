# Examples

Two modes, same result. Direct mode runs nvoi commands imperatively. Cloud mode pushes a config YAML and lets the API orchestrate.

## Dev workflow

```bash
# First time
make provision

# Run tests
make test

# Build
make build
```

## Direct mode (core)

Imperative commands — each line hits real infrastructure.

```bash
# Run any nvoi command
make cli CMD="instance list"
make cli CMD="describe"
make cli CMD="resources"

# Deploy to Hetzner (see examples/core/deploy-hetzner for the full sequence)
make cli CMD="instance set master --compute-type cx23 --compute-region fsn1"
make cli CMD="volume set pgdata --size 30 --server master"
make cli CMD="build --source benbonnet/dummy-rails --name web"
make cli CMD="secret set RAILS_MASTER_KEY abc123"
make cli CMD="storage set assets --cors"
make cli CMD="service set db --image postgres:17 --volume pgdata:/var/lib/postgresql/data --secret POSTGRES_PASSWORD"
make cli CMD="dns set web final.nvoi.to"

# Teardown
make cli CMD="dns delete web final.nvoi.to -y"
make cli CMD="service delete web -y"
make cli CMD="instance delete master -y"
```

## Cloud mode

Declarative config — push YAML, the API plans and executes.

```bash
# Login + setup context
make cloud CMD="login"
make cloud CMD="workspaces use default"
make cloud CMD="repos create my-app"
make cloud CMD="repos use my-app"

# Push config + env
make cloud CMD="push --config examples/cloud/hetzner.yaml --env .env --compute-provider hetzner --dns-provider cloudflare --storage-provider cloudflare --build-provider daytona"

# Preview the execution plan
make cloud CMD="plan"

# Deploy (streams live output — same TUI as direct mode)
make cloud CMD="deploy"

# Inspect live cluster
make cloud CMD="describe"
make cloud CMD="resources"

# Stream deployment logs
make cloud CMD="logs <deployment-id>"
```

## API server

```bash
# Start API + postgres
make api

# Health check
curl http://localhost:8080/health
```

## Example files

### `examples/core/` �� direct mode reference

| File | What it does |
|------|-------------|
| `deploy-hetzner` | Hetzner: 2 servers, 2 volumes, meilisearch, postgres, web, jobs |
| `deploy-aws` | AWS: 1 server, postgres, web, jobs |
| `deploy-scaleway` | Scaleway: 1 server, postgres, web, jobs |
| `deploy-full` | Hetzner with all flags explicit (zero env vars) |
| `deploy-router` | Reads COMPUTE_PROVIDER, delegates to the right script |
| `destroy` | Teardown in reverse order — idempotent deletes |

### `examples/cloud/` — cloud mode reference

| File | What it does |
|------|-------------|
| `hetzner.yaml` | Config YAML: 2 servers, managed postgres + meilisearch, web, jobs |
| `deploy-hetzner` | Login → push hetzner.yaml → plan → deploy |
| `aws.yaml` | Config YAML: 1 server, managed postgres, web, jobs |
| `deploy-aws` | Login → push aws.yaml → plan → deploy |
| `scaleway.yaml` | Config YAML: 1 server, managed postgres, web, jobs |
| `deploy-scaleway` | Login → push scaleway.yaml → plan → deploy |
| `destroy` | Delete the repo or push empty config |

The cloud configs use `managed: postgres` and `managed: meilisearch` — credentials are auto-generated, volumes auto-created, secrets auto-injected. The core scripts wire all of that by hand.
