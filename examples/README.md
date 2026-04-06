# Examples

Two modes, same result. Core runs nvoi commands imperatively. Cloud pushes a config YAML and lets the API orchestrate.

## Usage

```bash
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

All scripts use `bin/core` (direct CLI) or `bin/cloud` (cloud CLI). Provider selection is via `export` at the top of each script — `bin/core` and `bin/cloud` pass those through to compose.

## Structure

```
examples/
  core/                          direct mode — imperative bin/core commands
    hetzner/deploy               2 servers, 2 volumes, meilisearch, postgres, web, jobs
    hetzner/destroy              teardown in reverse order
    aws/deploy                   1 server, postgres, web, jobs
    aws/destroy                  teardown
    scaleway/deploy              1 server, postgres, web, jobs
    scaleway/destroy             teardown

  cloud/                         cloud mode — config YAML + bin/cloud push + deploy
    empty.yaml                   shared empty config for destroy-via-diff
    hetzner/config.yaml          managed postgres + meilisearch, web, jobs
    hetzner/deploy               login → push → plan → deploy
    hetzner/destroy              push empty → deploy (deletes everything)
    aws/config.yaml              managed postgres, web, jobs
    aws/deploy                   login → push → plan → deploy
    aws/destroy                  push empty → deploy
    scaleway/config.yaml         managed postgres, web, jobs
    scaleway/deploy              login → push → plan → deploy
    scaleway/destroy             push empty → deploy
```
