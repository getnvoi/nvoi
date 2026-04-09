# Examples

Two modes, same result. Core runs nvoi commands imperatively. Cloud pushes a config YAML and lets the API orchestrate.

## Usage

```bash
# Prepare example-only env once
cp examples/.env.example examples/.env

# Core mode — direct CLI, each command hits infrastructure
examples/core/hetzner/deploy
examples/core/aws/deploy
examples/core/scaleway/deploy

# Cloud mode — config YAML + push + deploy via API
examples/cloud/hetzner/deploy
examples/cloud/aws/deploy
examples/cloud/scaleway/deploy

# Teardown (reverse order)
examples/core/hetzner/destroy
examples/cloud/hetzner/destroy      # pushes empty config, diff deletes everything
```

All scripts use `bin/core` (direct CLI) or `bin/cloud` (cloud CLI). Example scripts read `examples/.env` only. The real app deploy path uses root `.env` only via `bin/deploy` and `bin/destroy`.

## Managed databases (local mode)

The `database` category command handles postgres setup, secrets, volumes, and backup in one command:

```bash
# Prerequisites
bin/core secret set POSTGRES_PASSWORD "$POSTGRES_PASSWORD"
bin/core storage set db-backups --expire-days 30

# Deploy managed postgres with backups
bin/core database set db --type postgres \
  --secret POSTGRES_PASSWORD \
  --backup-storage db-backups \
  --backup-cron "0 2 * * *"

# Backup operations
bin/core database backup create db --type postgres
bin/core database backup list db --type postgres
bin/core database backup download db --type postgres 2026-04-09-020000.sql.gz > backup.sql.gz

# Teardown
bin/core database delete db --type postgres -y
```

This replaces the manual `service set db --image postgres:17 --volume pgdata:/var/lib/postgresql/data --secret POSTGRES_PASSWORD ...` sequence. The compiler wires the namespaced secrets (POSTGRES_PASSWORD_DB, DATABASE_DB_*), volume, and backup cron.

## Managed databases (cloud mode)

In cloud config YAML, use `managed: postgres` on a service with `uses:` on consuming workloads:

```yaml
services:
  db:
    managed: postgres
  web:
    build: web
    port: 80
    uses: [db]    # injects DATABASE_DB_HOST, DATABASE_DB_PORT, etc.
```

The `uses: [db]` directive injects all exported secrets from the managed bundle. No manual secret aliasing needed.

## Structure

```
examples/
  core/                          direct mode — imperative bin/core commands
    hetzner/deploy               2 servers, 2 volumes, postgres, web, jobs
    hetzner/destroy              teardown in reverse order
    aws/deploy                   1 server, postgres, web, jobs
    aws/destroy                  teardown
    scaleway/deploy              1 server, postgres, web, jobs
    scaleway/destroy             teardown

  cloud/                         cloud mode — config YAML + bin/cloud push + deploy
    empty.yaml                   shared empty config for destroy-via-diff
    hetzner/config.yaml          managed postgres, web, jobs
    hetzner/deploy               login -> push -> plan -> deploy
    hetzner/destroy              push empty -> deploy (deletes everything)
    aws/config.yaml              managed postgres, web, jobs
    aws/deploy                   login -> push -> plan -> deploy
    aws/destroy                  push empty -> deploy
    scaleway/config.yaml         managed postgres, web, jobs
    scaleway/deploy              login -> push -> plan -> deploy
    scaleway/destroy             push empty -> deploy
```
