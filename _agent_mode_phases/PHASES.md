# Agent Mode — Phases

## Architecture

The agent is the deploy runtime. It runs on the master node. It holds credentials. It executes everything. The CLI and the API are clients — they send commands to the agent and stream output. Nothing else runs deploys.

```
Solo dev:    laptop CLI ──SSH──▶ agent (master)
Team:        laptop CLI ──SSH──▶ agent (master) ──reports──▶ API
CI:          runner CLI ──SSH──▶ agent (master) ──reports──▶ API
Dashboard:   browser ──▶ API (reads stored events from agent)
```

One execution path. CLI always SSHs to agent. Agent always does the work. API is an observer — receives events, serves dashboard, never in the command path.

### Bootstrap

Every user starts with the CLI. First `nvoi deploy` creates the server, installs the agent, deploys everything. The CLI executes directly during this first deploy — the agent doesn't exist yet. After provisioning completes, the agent is running and all subsequent operations go through it.

API is opt-in, always after bootstrap. The user signs up, connects their agent to the API, and the dashboard starts receiving events. There is no path where the API creates infrastructure or holds credentials — not even temporarily.

### Binary distribution

Same binary for CLI and agent. `nvoi agent` starts the server, `nvoi deploy` sends commands. One artifact, one release pipeline, zero version skew. Installed on the master during provisioning via `curl -fsSL https://get.nvoi.to | sh` — same installer users run on their laptops. Binary is cross-compiled per release and served from R2 via `cmd/distribution`.

### Agent reports all commands to the API

When connected to the API, the agent reports every command it executes — deploy, teardown, describe, logs, exec, cron, db. The API stores the full JSONL event stream per command in CommandLog. The dashboard surfaces this as deploy history and audit trail. If the API is down, deploys still work; events are queued and sent on reconnect.

---

## Phase 1: Agent package + subcommand

Same binary. Agent logic lives in `internal/agent/`. Started via `nvoi agent` subcommand.

The agent is a long-running HTTP server on the master (localhost only). It reads `nvoi.yaml` and `.env` from its working directory. It accepts commands and streams JSONL responses. All commands are reported to the API (when connected) for audit logging.

### New files

```
internal/agent/
  agent.go          — Agent struct, HTTP handlers, routing, streaming output
  credentials.go    — credentialSource(), BuildDeployContext(), resolveProviderCreds()
                      (moved from cmd/cli/local.go)

cmd/cli/
  agent.go          — cobra command: `nvoi agent`, starts internal/agent server
```

### What moves

From `cmd/cli/local.go` to `internal/agent/credentials.go`:
- `credentialSource()`
- `buildDeployContext()` → `BuildDeployContext()` (exported)
- `resolveProviderCreds()`
- `resolveSSHKey()`
- `resolveGitAuth()`
- `resolveDatabaseCreds()`

From `cmd/cli/local.go` (localBackend methods) to `internal/agent/agent.go` (HTTP handlers):
- `Deploy()` → `POST /deploy` → calls `reconcile.Deploy()`, streams JSONL
- `Teardown()` → `POST /teardown`
- `Describe()` → `GET /describe`
- `Logs()` → `GET /logs/{service}`
- `Exec()` → `POST /exec/{service}`
- `SSH()` → `POST /ssh`
- `CronRun()` → `POST /cron/{name}/run`
- `DatabaseBackupList()` → `GET /db/{name}/backups`
- `DatabaseBackupDownload()` → `GET /db/{name}/backups/{key}`
- `DatabaseSQL()` → `POST /db/{name}/sql`
- `POST /config` → push new nvoi.yaml, agent reloads
- `GET /health` → agent status

### Handler contract

Every handler:
1. Reads request params
2. Calls the corresponding `pkg/core` or `reconcile` function
3. Streams JSONL events via chunked HTTP response (same format as today's API streaming)
4. Returns final status

The agent holds a single `DeployContext` built at startup. Commands that need a fresh context (deploy, teardown) rebuild it.

### Agent config

The agent reads from its working directory:
- `nvoi.yaml` — app config
- `.env` — credentials (or uses secrets provider as configured in nvoi.yaml)

Started via systemd. Working directory: `/opt/nvoi/{app}-{env}/`.

---

## Phase 2: Agent provisioning

The agent binary installs on the master during `ServersAdd`, same as k3s, Docker, swap.

### New files

```
pkg/infra/
  agent.go          — InstallAgent(): upload binary, config, env, systemd unit, start
```

### InstallAgent() flow

1. Upload agent binary to master via SFTP (`/usr/local/bin/nvoi-agent`)
2. Upload `nvoi.yaml` to `/opt/nvoi/{app}-{env}/nvoi.yaml`
3. Upload `.env` to `/opt/nvoi/{app}-{env}/.env`
4. Generate and upload systemd unit to `/etc/systemd/system/nvoi-agent.service`
5. `systemctl daemon-reload && systemctl enable --now nvoi-agent`

### Systemd unit

```ini
[Unit]
Description=nvoi agent
After=network.target

[Service]
Type=simple
WorkingDirectory=/opt/nvoi/{app}-{env}
ExecStart=/usr/local/bin/nvoi-agent
Restart=always
RestartSec=5
User=deploy

[Install]
WantedBy=multi-user.target
```

### Binary distribution

Same binary as the CLI. During provisioning, the nvoi binary is uploaded to the master and started with `nvoi agent`. No separate build, no version skew. The binary is cross-compiled for the target arch (arm64/amd64) — same as the existing release pipeline (R2 distribution).

### Integration into ComputeSet

After `InstallK3sMaster` + `EnsureRegistry`:
```
→ InstallAgent(ctx, ssh, cfg, envFile)
```

InstallAgent:
1. Upload nvoi binary to `/usr/local/bin/nvoi`
2. Upload `nvoi.yaml` to `/opt/nvoi/{app}-{env}/nvoi.yaml`
3. Upload `.env` to `/opt/nvoi/{app}-{env}/.env`
4. Generate + upload systemd unit
5. `systemctl enable --now nvoi-agent`

The agent starts, binds to localhost:9500, ready for commands.

---

## Phase 3: CLI as thin client

The CLI becomes a command sender. It SSHs into the master, port-forwards to the agent's local port, sends commands, streams output.

### Rewritten files

```
cmd/cli/
  main.go           — rootCmd, SSH connection setup, no backend interface
  connect.go        — SSH to master, port-forward to agent, return HTTP client
  deploy.go         — POST /deploy via tunnel, stream JSONL
  teardown.go       — POST /teardown via tunnel, stream JSONL
  describe.go       — GET /describe via tunnel
  logs.go           — GET /logs/{service} via tunnel
  exec.go           — POST /exec/{service} via tunnel
  ssh.go            — POST /ssh via tunnel
  cron.go           — POST /cron/{name}/run via tunnel
  database.go       — db commands via tunnel
```

### Deleted files

```
cmd/cli/
  local.go          — gone. Agent handles execution.
  backend.go        — gone. No Backend interface.
  cloud.go          — gone. No cloudBackend.
```

### connect.go

The CLI needs to reach the agent. In solo mode (no API):
1. Read `nvoi.yaml` to get compute provider + server definitions
2. Read `.env` to get compute credentials
3. Call provider API to resolve master IP (same as today's `DescribeLive`)
4. SSH into master using SSH key from `.env`
5. Port-forward localhost:{agent_port} on the master to the CLI
6. Return an HTTP client pointed at the tunnel

Every command goes through this tunnel. One SSH connection per CLI invocation, reused across commands.

### Command pattern

Every command file follows the same pattern:

```go
func newDeployCmd() *cobra.Command {
    return &cobra.Command{
        Use: "deploy",
        RunE: func(cmd *cobra.Command, args []string) error {
            client, cleanup, err := connect(cmd.Context())
            if err != nil { return err }
            defer cleanup()
            return streamCommand(client, "POST", "/deploy", nil, resolveOutput(cmd))
        },
    }
}
```

`streamCommand` reads chunked JSONL from the response and replays through the output interface. Same JSONL format, same renderers (TUI, plain, JSON).

### Config push

Before sending a deploy command, the CLI pushes the local `nvoi.yaml` to the master via the SSH connection. The agent reloads config on each deploy request. This ensures the agent always has the latest config from the user's laptop.

`.env` push: `nvoi env push` command. Uploads `.env` to the agent's working directory. Used when the user updates credentials locally.

---

## Phase 4: API as control plane

The API stores audit logs, teams, deploy history. The agent reports to it. The CLI never talks to the API for deploys — it always SSHs to the agent directly. The API is an observer, not a relay.

### Agent → API connection

The agent, if configured, connects outbound to the API via WebSocket. It reports events — it does NOT receive commands from the API. Commands come from the CLI over SSH.

```
cmd/agent/
  api.go            — outbound WebSocket to API, report events, sync status
```

Agent config (in `.env` or nvoi.yaml):
```
NVOI_API_BASE=https://api.nvoi.to
NVOI_AGENT_TOKEN=xxx
```

If `NVOI_API_BASE` is not set, the agent runs standalone. No API connection. Solo mode.

If set, the agent:
1. Connects WebSocket to `wss://api.nvoi.to/ws/agent`
2. Authenticates with `NVOI_AGENT_TOKEN`
3. Registers: workspace, repo, status
4. Reports ALL commands to the API — deploy, teardown, describe, logs, exec, cron, db. Every command the agent executes gets logged to the API with full JSONL event stream.
5. API stores events in CommandLog, surfaces on dashboard
6. Reconnects on disconnect (exponential backoff)

The API is never in the deploy path. If the API is down, deploys still work. The agent queues events and sends them when the API comes back. The API has a complete audit trail of every operation on every cluster.

### CLI never talks to the API for commands

The CLI always SSHs to the agent. Solo dev or team — same path. The CLI does not need API auth to deploy. API auth is only for dashboard, team management, audit viewing.

```
nvoi deploy     → SSH to agent → agent executes → agent reports to API
nvoi describe   → SSH to agent → agent executes → agent reports to API
```

The API sees everything but controls nothing.

### API changes

#### New files
```
internal/api/handlers/
  agents.go         — WebSocket endpoint for agent connections, event ingestion, status tracking
```

#### Rewritten files
```
internal/api/handlers/
  router.go         — new routes: /ws/agent, GET /deploys, GET /agents. Remove provider routes, deploy/teardown execution routes.
```

#### Deleted files
```
internal/api/handlers/
  providers.go      — gone. No credential storage.
  deploy.go         — gone. API doesn't execute or dispatch deploys.
  teardown.go       — gone. Same.
  describe.go       — gone. CLI talks to agent directly.
  ssh.go            — gone. CLI talks to agent directly.
  cron.go           — gone.
  storage.go        — gone.
  dns.go            — gone.
```

The API becomes read-only for deploy data. It receives events from agents. It serves the dashboard. It does not trigger, dispatch, or execute anything.

#### Models

```
internal/api/models.go:
  DELETE: InfraProvider
  DELETE: Repo.ComputeProviderID, DNSProviderID, StorageProviderID, BuildProviderID, SecretsProviderID
  DELETE: Provider foreign keys and relations
  ADD: Agent { ID, WorkspaceID, RepoID, Token, LastSeen, Status }
  KEEP: User, Workspace, WorkspaceUser, Repo (slimmed), CommandLog
```

#### Agent token management
```
internal/api/handlers/
  agent_tokens.go   — create/revoke agent tokens, scoped to workspace+repo
```

CLI commands for agent setup:
```
nvoi agent token create    — generates token, displays once
nvoi agent token revoke    — revokes token
```

### Deleted packages

```
internal/cloud/          — entire package gone
  backend.go             — cloudBackend gone
  client.go              — gone (CLI no longer talks to API for commands)
  auth.go                — stays (for agent token management only)
  login.go               — stays
  provider.go            — gone
  repos.go               — gone
  workspaces.go          — stays
  whoami.go              — stays
```

---

## Phase 5: Cleanup

Delete everything that's dead.

### Files to delete
- `cmd/cli/local.go`
- `cmd/cli/backend.go`
- `cmd/cli/cloud.go` (if separate from rewritten commands)
- `internal/cloud/backend.go`
- `internal/cloud/provider.go`
- `internal/api/handlers/providers.go`
- Credential encryption utilities
- Any migration files for InfraProvider (replaced by drop migration)

### DB migration
- Drop `infra_providers` table
- Drop provider foreign key columns from `repos`
- Add `agents` table

### go.mod cleanup
- Remove unused dependencies
- `go mod tidy`

### CLAUDE.md update
- Architecture section rewritten around agent model
- CLI section updated (thin client)
- API section updated (control plane, zero credentials)
- Commands section updated
- Remove Backend interface docs
- Add agent provisioning docs
- Add agent commands docs

---

## Execution order

Phase 1 and 2 can be built together — the agent binary and its provisioning are coupled.

Phase 3 depends on phase 1 — the CLI needs the agent to exist.

Phase 4 depends on phase 1 and 3 — the API control plane needs the agent and the new CLI.

Phase 5 is after everything works.

```
Phase 1+2 (agent + provisioning)
    ↓
Phase 3 (CLI rewrite)
    ↓
Phase 4 (API control plane)
    ↓
Phase 5 (cleanup)
```
