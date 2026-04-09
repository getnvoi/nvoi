# Examples

Two modes, same commands. Core runs directly against infrastructure. Cloud accumulates config on the API, then deploys.

## Usage

```bash
# Prepare example-only env once
cp examples/.env.example examples/.env

# Core mode — direct CLI, each command hits infrastructure immediately
examples/core/hetzner/deploy
examples/core/aws/deploy
examples/core/scaleway/deploy

# Cloud mode — same commands, config accumulates on API, deploy at the end
examples/cloud/hetzner/deploy
examples/cloud/aws/deploy
examples/cloud/scaleway/deploy

# Cloud mode via YAML push (alternative declarative approach)
examples/cloud/hetzner-yaml/deploy

# Teardown (reverse order)
examples/core/hetzner/destroy
examples/cloud/hetzner/destroy
```

## Managed databases (both modes)

```bash
# Prerequisites
nvoi secret set POSTGRES_PASSWORD "$POSTGRES_PASSWORD"
nvoi secret set POSTGRES_USER "$POSTGRES_USER"
nvoi secret set POSTGRES_DB "$POSTGRES_DB"
nvoi storage set db-backups --expire-days 30

# Deploy managed postgres with backups
nvoi database set db --type postgres \
  --secret POSTGRES_PASSWORD \
  --secret POSTGRES_USER \
  --secret POSTGRES_DB \
  --backup-storage db-backups \
  --backup-cron "0 2 * * *"

# Backup operations
nvoi database backup create db --type postgres
nvoi database backup list db --type postgres
nvoi database backup download db --type postgres backup-20260409.sql.gz > backup.sql.gz

# Teardown
nvoi database delete db --type postgres
```

Works identically with `bin/core` (direct) or `bin/cloud` (cloud + deploy).

## Structure

```
examples/
  core/                          direct mode — bin/core commands, immediate execution
    hetzner/deploy               2 servers, managed postgres, web, jobs, cloudflare-managed
    hetzner/destroy              teardown in reverse order
    aws/deploy                   1 server, managed postgres, web, jobs
    aws/destroy                  teardown
    scaleway/deploy              1 server, managed postgres, web, jobs
    scaleway/destroy             teardown

  cloud/                         cloud mode — bin/cloud commands, deploy at the end
    hetzner/deploy               same as core/hetzner but via API
    hetzner/destroy              reverse order + deploy
    aws/deploy                   same as core/aws but via API
    aws/destroy                  reverse order + deploy
    scaleway/deploy              same as core/scaleway but via API
    scaleway/destroy             reverse order + deploy

    hetzner-yaml/                alternative: YAML config push approach
      config.yaml                declarative config
      empty.yaml                 empty config for destroy-via-diff
      deploy                     push config → plan → deploy
      destroy                    push empty → deploy
```
