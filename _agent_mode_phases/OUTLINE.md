# Agent Mode — Full Restructure Plan

## The architecture

The agent is how nvoi works. It runs on the master node, installed during provisioning. It holds credentials, runs deploys, controls the cluster. Everything else is a client.

```
┌─────────────┐     ┌─────────────┐
│  CLI (laptop)│     │  API (cloud) │
│  nvoi deploy │     │  dashboard   │
└──────┬───────┘     └──────┬───────┘
       │ SSH tunnel         │ outbound WS
       │                    │
       └────────┬───────────┘
                │
         ┌──────▼──────┐
         │    Agent     │
         │  (master)    │
         │              │
         │ credentials  │
         │ reconcile    │
         │ kubectl      │
         └──────────────┘
```

- **Agent**: long-running process on master. Holds credentials (env or secrets provider). Runs reconcile.Deploy(), teardown, describe, all operations. Listens for commands from CLI (SSH) and API (outbound WebSocket).
- **CLI**: thin client. Sends commands to the agent. Streams output. Never resolves credentials, never runs reconcile directly.
- **API**: control plane. Dashboard, team management, audit log, deploy history. Dispatches commands to the agent via the outbound WebSocket. Zero credential storage, zero execution.

No tiers. No migration. No backward compatibility with the current model.

## What gets killed

### cmd/cli
- `localBackend` — gone. CLI doesn't execute deploys.
- `cloudBackend` — gone. CLI doesn't relay through API.
- `Backend` interface — gone. One execution model.
- `buildDeployContext()` — moves to agent.
- `credentialSource()` — moves to agent.
- `resolveProviderCreds()`, `resolveSSHKey()`, `resolveDatabaseCreds()` — all move to agent.
- `--local` flag — gone. Replaced by `--direct` or similar (SSH to agent directly vs through API).
- viper (already removed) — stays removed.
- Mode detection (`initLocal`, `initCloud`) — gone.

### cmd/api (API server)
- Server-side deploy execution — gone. API never calls reconcile.Deploy().
- `InfraProvider` model + credential storage — gone. API stores zero credentials.
- `nvoi provider add/list/delete` — gone. No credentials to add.
- Credential encryption layer — gone.
- `resolveRepoCreds()`, `credentialSourceFromRepo()` — gone.
- Deploy/teardown handlers become dispatch-to-agent, not execute-locally.

### internal/cloud
- `cloudBackend` streaming — gone. CLI talks to agent, not API.
- `StreamRun` — gone or repurposed for agent streaming.

### internal/api/models.go
- `InfraProvider` model — gone.
- `Repo.ComputeProviderID`, `DNSProviderID`, `StorageProviderID`, `BuildProviderID`, `SecretsProviderID` — gone.
- Provider foreign keys — gone.

### internal/api/handlers
- `providers.go` — gone entirely.
- Deploy/teardown/describe handlers — rewritten as dispatch-to-agent.

## What stays

### pkg/core, pkg/kube, pkg/infra, pkg/provider, pkg/utils
Untouched. The deploy engine doesn't change. It just runs on the agent instead of the CLI/API.

### internal/reconcile
Untouched. `Deploy()`, `Teardown()`, all reconcile steps — same code, runs on agent.

### internal/config
`AppConfig`, `DeployContext`, `CredentialSource` — same. Used by agent.

### internal/packages
Database package, all packages — same. Runs on agent.

## What gets built

### Phase 1: Agent binary on master

**Goal**: `nvoi agent` runs on the master, accepts commands over a local interface, executes them.

Files:
- `cmd/agent/main.go` — agent entrypoint. Reads config, resolves credentials (the code currently in cmd/cli/local.go), starts command listener.
- `cmd/agent/server.go` — HTTP server on localhost (or Unix socket). Accepts command requests, runs them, streams JSONL responses.
- `cmd/agent/credentials.go` — credential resolution moved from cmd/cli/local.go. `credentialSource()`, `buildDeployContext()`, `resolveProviderCreds()`, etc.
- `cmd/agent/handlers.go` — deploy, teardown, describe, logs, exec, ssh, cron, database handlers. Each calls the corresponding pkg/core or reconcile function.

The agent reads `nvoi.yaml` and `.env` from its working directory on the master. Same files, same format. Provisioning copies them to the master during setup.

### Phase 2: CLI as thin client over SSH

**Goal**: `nvoi deploy` from your laptop SSHs into the master, hits the agent, streams output.

Files:
- `cmd/cli/main.go` — rewritten. No backend interface. All commands SSH to agent.
- `cmd/cli/deploy.go` — SSH to master → POST to agent's local endpoint → stream JSONL.
- `cmd/cli/teardown.go`, `describe.go`, `logs.go`, `exec.go`, etc. — same pattern.
- `cmd/cli/connect.go` — SSH connection to master, port-forward to agent's local listener.

The CLI needs: SSH key (to reach the master) and the master's IP. That's it. No provider credentials, no config parsing beyond finding the master.

How the CLI knows the master IP: from `nvoi.yaml` (already has server definitions) + provider API to resolve IP. Or: cached from last deploy. Or: stored in a lightweight local state file (just the IP).

### Phase 3: Agent provisioning

**Goal**: `nvoi agent` installs automatically during `ServersAdd`.

The agent binary is deployed to the master during provisioning, alongside k3s, Docker, swap. Config and credentials are uploaded via SSH (SFTP, same as today).

Files:
- `pkg/infra/agent.go` — `InstallAgent()`: upload binary, upload config, upload .env, install systemd service, start.
- `pkg/infra/agent_service.go` — systemd unit generation for the agent.
- Provisioning flow in `ComputeSet` gains an `InstallAgent` step after k3s master setup.

The agent binary: same Go binary, different entrypoint. `nvoi agent` starts the server. Cross-compiled for the target arch during build (same as today's build flow).

### Phase 4: API as control plane + outbound WebSocket

**Goal**: API dispatches commands to agents. Dashboard, team, audit.

Files:
- `cmd/agent/api_connect.go` — outbound WebSocket to API. Agent registers: "I'm workspace X, repo Y, ready." Polls for commands. Sends results.
- `internal/api/handlers/dispatch.go` — deploy/teardown/describe handlers rewritten. Find connected agent for repo, send command, relay results to client.
- `internal/api/handlers/agents.go` — WebSocket endpoint for agent connections. Track connected agents.
- `internal/api/handlers/router.go` — updated routes. Provider routes removed.
- `internal/api/models.go` — stripped. No InfraProvider. Repo links to agent, not to providers.
- `internal/api/models.go` — new: `Agent` model (workspace, repo, last_seen, status).
- `internal/api/models.go` — new: `CommandLog` gains agent_id.

API config: agent connects with a token. `nvoi agent token` generates one. Token stored in the `Agent` model. Scoped to workspace + repo.

### Phase 5: Kill the dead code

- Delete `internal/cloud/` — entire package.
- Delete `internal/api/handlers/providers.go`.
- Delete credential encryption utilities.
- Delete `InfraProvider` model, migration to drop the table.
- Delete `Repo` provider foreign keys, migration.
- Delete `Backend` interface from cmd/cli.
- Delete `localBackend`, `cloudBackend`.
- Clean up go.mod — remove unused deps.

## Provisioning flow (new)

```
nvoi deploy (first time, from laptop)
  → no master exists yet
  → CLI reads nvoi.yaml, resolves compute creds LOCALLY (one-time bootstrap)
  → creates master server at provider
  → SSHs into master
  → installs k3s, Docker, swap (existing)
  → installs agent binary
  → uploads nvoi.yaml + .env to master
  → starts agent systemd service
  → agent takes over — runs the rest of the deploy
  → CLI connects to agent for remaining output
  → subsequent deploys: CLI → SSH → agent (no local credential resolution)
```

The first deploy is special: the CLI must resolve compute credentials locally to create the master. After that, everything runs on the agent. The `.env` on the user's laptop is only needed once — for initial provisioning. After that, credentials live on the master.

## Open questions

1. **Agent binary distribution**: cross-compile during nvoi build? Download from R2 during provisioning? Same binary as CLI with a different command?
2. **Config sync**: when user edits nvoi.yaml locally, how does it reach the agent? `nvoi config push` over SSH? Auto-sync on deploy command?
3. **Credential updates**: user rotates a token in Infisical. Agent picks it up via SecretsSource on next deploy. But if creds are in .env on the master, user needs to update that file. `nvoi env push` command?
4. **Multiple repos per master**: one agent per repo, or one agent serving multiple repos?
5. **Agent updates**: when nvoi releases a new version, how does the agent binary update? `nvoi agent upgrade` from laptop?
6. **First deploy bootstrap**: the CLI needs compute creds locally just once. Should it prompt? Read from .env? Accept as flags?
