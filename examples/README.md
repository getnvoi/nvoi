# Examples

Two modes, same result. Core runs nvoi commands imperatively via compose. Cloud pushes a config YAML and lets the API orchestrate.

## Usage

```bash
# Deploy
./examples/deploy core hetzner
./examples/deploy core aws
./examples/deploy core scaleway
./examples/deploy cloud hetzner
./examples/deploy cloud aws
./examples/deploy cloud scaleway

# Destroy
./examples/destroy core hetzner
./examples/destroy core aws
./examples/destroy core scaleway
./examples/destroy cloud hetzner
./examples/destroy cloud aws
./examples/destroy cloud scaleway
```

## Structure

```
examples/
  deploy                         wrapper — ./examples/deploy <core|cloud> <provider>
  destroy                        wrapper — ./examples/destroy <core|cloud> <provider>

  core/                          direct mode — imperative $CLI commands
    hetzner/deploy               2 servers, 2 volumes, meilisearch, postgres, web, jobs
    hetzner/destroy              teardown in reverse order
    aws/deploy                   1 server, postgres, web, jobs
    aws/destroy                  teardown
    scaleway/deploy              1 server, postgres, web, jobs
    scaleway/destroy             teardown

  cloud/                         cloud mode — config YAML + push + deploy
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

## How the wrappers work

The wrappers export `$CLI` and call the per-provider script:

- `core` mode: `CLI="docker compose run --rm core"` — direct CLI, each command hits infrastructure
- `cloud` mode: `CLI="docker compose run --rm cli"` — cloud CLI, talks to the API

Each per-provider script uses `$CLI` for all commands. The `# nvoi ...` comment above each line shows the real-world equivalent.
