# CLAUDE.md — nvoi

## What nvoi is

A CLI that deploys containers to cloud servers from a declarative YAML config. `nvoi deploy` reconciles live infrastructure to match the config. `nvoi teardown` nukes it all. `nvoi describe` fetches everything live from the cluster.

## Philosophy

- **`app` + `env` is the namespace.** Defined in `nvoi.yaml`. `nvoi-{app}-{env}-*`. Different app or env = brand new infrastructure.
- **No state files.** No manifest, no database, no local cache. Infrastructure is the source of truth.
- **Everything is idempotent.** `nvoi deploy` reconciles: adds desired resources, removes orphans. Run twice, same result.
- **Naming is the lookup key.** `nvoi-{app}-{env}-{resource}`. Deterministic. No UUIDs. The naming convention finds everything.
- **Reconcile vs teardown.** Reconcile converges on a diff: queries live state, adds what's missing, removes what's orphaned — manages everything (provider infra and k8s resources). Teardown is a hard nuke: no diff, no live state query, wipes all external provider resources. K8s resources die with the servers. Volumes and storage preserved by default — `--delete-volumes` / `--delete-storage` to nuke.
- **Declarative config, imperative reconciliation.** `nvoi.yaml` declares desired state. The reconciler walks each resource type in order.
- **Packages.** Higher-level abstractions that bundle infra + secrets + CLI. `database:` is the first package — creates StatefulSet, headless Service, credentials, backup bucket, backup CronJob from one config block. Packages hook into the reconcile loop between secrets and storage.
- **Provider interfaces scale.** Hetzner, Cloudflare, AWS, Scaleway. Interface-first. Add a provider = implement the interface. Organized by domain: `compute/`, `dns/`, `storage/`, `build/`.
- **Agent is the deploy runtime.** The agent runs on the master node, holds credentials, executes all operations via client-go. The CLI is a thin client that sends commands to the agent. The API is a control plane that receives events.
- **Secrets provider.** When `providers.secrets` is configured (doppler, awssm, infisical), all credentials (infra + app) are fetched from the user's secrets provider. Bootstrap creds in `.env`, everything else in the vault. When not configured, credentials come from env vars directly.

## Build & Test

```bash
bin/test                   # enforced 5s timeout — MUST pass, no exceptions
bin/test -v                # verbose
go test ./... -cover       # coverage
go build ./cmd/cli
```

**Test suite MUST complete in under 2 seconds per package.** `bin/test` enforces this with `go test -timeout 2s`. Any test that exceeds this is broken — fix it by injecting mocks for I/O waits (SSH polls, HTTP retries, stability delays). Never sleep in tests. Override production timeouts with `kube.SetTestTiming(time.Millisecond, time.Millisecond)` in test `init()`. This is non-negotiable.

## CI

GitHub Actions workflows (`.github/workflows/`):

- **ci.yml** — fmt + vet + test + build on push and PR
- **deploy.yml** — production deploy on push to main (runs `bin/deploy`)
- **release.yml** — cross-compile on git tags (`v*`), upload to R2

**PR merges:** Never squash. Use `gh pr merge --merge --delete-branch`.

## Local development

`bin/nvoi` is the universal entrypoint. Sources `.env`, builds the binary if needed, runs any command.

```bash
# Deploy / teardown
bin/nvoi deploy                # reconcile from nvoi.yaml
bin/nvoi teardown              # nuke provider resources
bin/deploy                     # shorthand for bin/nvoi deploy
bin/destroy                    # shorthand for bin/nvoi teardown

# Operate
bin/nvoi describe              # live cluster state
bin/nvoi logs web              # stream logs
bin/nvoi logs api -f           # follow logs
bin/nvoi exec web -- sh        # shell into service pod
bin/nvoi ssh -- kubectl get pods  # run command on master

# Database
bin/nvoi db sql "SELECT 1;"   # run SQL
bin/nvoi db backup now         # trigger backup
bin/nvoi db backup list        # list backups in R2
bin/nvoi db backup download <name> -f ./backup.sql.gz

# Cron
bin/nvoi cron run db-backup    # trigger cron job

# Agent
bin/nvoi agent                 # start agent server on master (systemd manages this)

# Inspect
bin/nvoi resources             # list all provider resources
go test ./...                  # run tests
```

### Deploy flow

`nvoi deploy` detects whether the agent is running on the master:

1. **Agent reachable** → SSH tunnel to agent → agent executes deploy → streams JSONL
2. **No master exists** → prompt "Create servers?" (bypass with `-y`) → bootstrap locally → install agent → subsequent deploys go through agent

No `--local` flag. No mode detection. One command, transparent.

### Files

| File | Purpose |
|------|---------|
| `nvoi.yaml` | Infrastructure config (tracked) |
| `.env` | Provider credentials + app secrets (not tracked) |
| `bin/nvoi` | Universal entrypoint — sources .env, builds `cmd/cli`, runs |
| `bin/deploy` | Shorthand for `bin/nvoi deploy` |
| `bin/destroy` | Shorthand for `bin/nvoi teardown` |

## Config format

```yaml
app: myapp
env: production

providers:
  compute: hetzner          # hetzner | aws | scaleway
  dns: cloudflare           # cloudflare | aws | scaleway
  storage: cloudflare       # cloudflare | aws | scaleway
  build: local              # local | daytona | github
  secrets: infisical        # doppler | awssm | infisical (optional)

servers:
  master:
    type: cax11
    region: nbg1
    role: master
    disk: 50                   # root disk GB (optional, AWS + Scaleway only)

firewall: default            # string or list of port:cidr rules

volumes:
  pgdata:
    size: 20
    server: master

database:                    # package — bundles StatefulSet, Service, credentials, backup
  main:
    kind: postgres           # required — postgres or mysql
    image: postgres:17       # required — container image
    volume: pgdata           # required — references defined volume

secrets:                     # user secrets, resolved from CredentialSource
  - JWT_SECRET
  - ENCRYPTION_KEY

storage:
  releases: {}

build:
  api: ./cmd/api

services:
  api:
    build: api
    port: 8080
    secrets: [JWT_SECRET, ENCRYPTION_KEY]
    server: master           # single node — nodeSelector
    # servers: [worker-1, worker-2]  # multi-node — nodeAffinity + topologySpread

crons:
  cleanup:
    image: busybox
    schedule: "0 1 * * *"
    command: echo hi

domains:
  api: [api.myapp.com]
```

### Validation

`ValidateConfig()` runs before touching infrastructure (includes package validation):

- `app` and `env` required
- `providers.compute` required
- `providers.secrets` if set: must be `doppler`, `awssm`, or `infisical`
- At least one server, exactly one master, all have type/region/role. `disk` optional (creation-only, not resizable). Hetzner + `disk` = hard error (fixed per server type).
- Volumes: size > 0, server exists
- Services/crons: image XOR build, referenced build/storage/volumes exist
- Volume mounts: `name:/path` format, volume must be on same server as workload
- `server` and `servers` mutually exclusive. Multiple servers + volume = error.
- Web-facing services (with domains): replicas omitted → defaults to 2. Explicit `replicas: 1` → hard error. 2 replicas on a single `server:` node is valid — the rule ensures process-level redundancy, not node distribution.
- Database: kind required (postgres or mysql), image and volume required, storage provider required, name collisions checked.

## Commands

```bash
nvoi deploy                              # reconcile to match config
nvoi deploy -y                           # skip confirmation on first deploy
nvoi teardown                            # nuke external provider resources
nvoi teardown --delete-volumes --delete-storage
nvoi describe                            # live cluster state
nvoi resources                           # list all provider resources
nvoi logs <service>                      # stream service logs
nvoi logs <service> -f                   # follow logs
nvoi exec <service> -- cmd               # run command in service pod
nvoi ssh -- cmd                          # run command on master node
nvoi cron run <name>                     # trigger cron job immediately
nvoi db backup now                       # trigger database backup
nvoi db backup list                      # list backups in bucket
nvoi db backup download <name> [-f file] # download backup
nvoi db sql "SELECT ..."                 # run SQL on database pod
nvoi agent                               # start agent server (master node)
```

Global flags: `--config` (default: `nvoi.yaml`), `--json` (JSONL output), `--ci` (plain text), `-y` (skip prompts).

## Architecture

```
cmd/
  cli/                     CLI entrypoint — thin client to agent
    main.go                rootCmd, initBackend (agent or bootstrap)
    backend.go             Backend interface (12 methods)
    local.go               localBackend — bootstrap path (first deploy)
    agent.go               nvoi agent subcommand — starts agent server
    agent_client.go        agentBackend — SSH tunnel to agent, JSONL streaming
    deploy.go..ssh.go      One file per command, backend-agnostic
  api/main.go              API server entrypoint (control plane)
  distribution/main.go     Binary distribution server (R2-backed)

internal/
  agent/                   Agent — deploy runtime on the master node
    agent.go               HTTP server, CommandFunc handlers, JSONL-only output
    credentials.go         CredentialSource, BuildDeployContext, provider resolution
  config/                  Shared types — no logic
    config.go              AppConfig, DeployContext, LiveState, all definition types
  reconcile/               Deploy orchestrator — YAML to infrastructure
    reconcile.go           Deploy() — ordered reconciliation with packages phase
    validate.go            ValidateConfig() — fail-fast pre-flight checks
    helpers.go             DescribeLive(), SplitServers(), ResolveServers()
    infra_servers.go       ServersAdd + ServersRemoveOrphans
    infra_firewall.go      Firewall reconciliation
    infra_volumes.go       Volume reconciliation
    infra_build.go         Build reconciliation
    infra_storage.go       Storage reconciliation
    infra_dns.go           DNS reconciliation
    runtime_secrets.go     Secret reconciliation (CredentialSource, no viper)
    runtime_services.go    Service reconciliation (KubeClient for secrets + apply)
    runtime_crons.go       Cron reconciliation
    runtime_ingress.go     Ingress reconciliation
  packages/                Package interface and registry
    package.go             Package interface, ValidateAll, ReconcileAll, TeardownAll
    database/              Database package — postgres + mysql engine support
  core/                    Source-agnostic logic — teardown, database helpers
    teardown.go            Teardown() — ordered resource deletion
    database.go            DatabaseBackupList, DatabaseBackupDownload, DatabaseSQL
  api/                     REST API server — control plane only
    models.go              User, Workspace, WorkspaceUser, Repo, CommandLog
    db.go                  PostgreSQL + AutoMigrate
    handlers/              Auth, workspaces, repos, config (no execution handlers)
  render/                  Output renderers — TUI, Plain, JSON
  testutil/                MockSSH, MockCompute, MockDNS, MockBucket, MockOutput

pkg/
  core/                    Business logic. One file per domain. No cobra, no I/O, no stdout.
    cluster.go             Cluster struct (Kube *KubeClient, MasterSSH), ProviderRef
    compute.go             ComputeSet (SSH connect, Docker, k3s, label), ComputeDelete
    service.go             ServiceSet, ServiceDelete — uses Kube for apply + secrets
    dns.go                 DNSSet, DNSDelete, DNSList
    ingress.go             IngressSet, IngressDelete — uses Kube for apply
    storage.go             StorageSet, StorageDelete, StorageEmpty, StorageList
    secret.go              SecretList, SecretReveal — uses Kube for secret reads
    volume.go              VolumeSet, VolumeDelete, VolumeList
    build.go               BuildRun, BuildParallel, BuildList, BuildLatest, BuildPrune
    cron.go                CronSet, CronDelete, CronRun — uses Kube for jobs
    database.go            DatabaseBackupList, DatabaseBackupDownload, DatabaseSQL
    describe.go            Describe — uses Kube for cluster state
    resources.go           Resources
    firewall.go            FirewallSet, FirewallList
    wait.go                WaitRollout — uses Kube.WaitRolloutReady
    exec.go                Exec — uses Kube.ExecInPod (SPDY)
    ssh.go                 SSH
    logs.go                Logs — uses Kube.StreamLogs
  kube/                    K8s operations — client-go + YAML generation
    client.go              KubeClient: client-go native API (NewLocal, NewTunneled)
    generate.go            Service YAML generation (Deployment/StatefulSet)
    cron.go                CronJob YAML generation
    ingress.go             Ingress YAML generation
    rollout.go             ProgressEmitter, timing vars
    apply.go               LabelNode (bootstrap SSH only), PodSelector
    types.go               PodInfo, WorkloadItem, PodList (kubectl JSON compat)
  infra/                   SSH, server bootstrap, k3s, Docker, swap, volume mounting
    agent.go               InstallAgent, UpgradeAgent, PushConfig, PushEnv
  provider/                Provider interfaces + per-domain implementations
    compute.go             ComputeProvider interface
    dns.go                 DNSProvider interface
    bucket.go              BucketProvider interface
    builder.go             BuildProvider interface
    secrets.go             SecretsProvider interface (Get, List — read-only)
    resolve.go             CredentialSource (EnvSource, MapSource, SecretsSource), registration
    s3ops/                 Shared S3 operations
    compute/hetzner/aws/scaleway/
    dns/cloudflare/aws/scaleway/
    storage/cloudflare/aws/scaleway/
    build/local/daytona/github/
    secrets/doppler/awssm/infisical/
  utils/                   Pure utilities: naming, poll, httpclient, ssh keys
    naming.go              NvoiSelector, KubeconfigPath, UserKubeconfigPath, labels
    s3/                    S3-compatible operations with AWS Signature V4
```

### Agent model

The agent (`internal/agent/`) is the deploy runtime. It runs on the master node as a long-running HTTP server (localhost:9500). All k8s operations go through `client-go` — no kubectl binary, no SSH for k8s.

**Agent handlers** receive `*jsonlOutput` (not `http.ResponseWriter`). They cannot write anything except JSONL events. This is enforced by the `CommandFunc` type signature. Every endpoint returns JSONL.

**KubeClient** (`pkg/kube/client.go`) wraps `client-go` clientset + dynamic client. Two constructors:
- `NewLocal(path)` — agent on master, reads kubeconfig directly
- `NewTunneled(ctx, ssh)` — CLI bootstrap, routes through SSH tunnel to master:6443

**Cluster.Kube** is always set before any k8s operation. The reconcile loop creates it via `NewTunneled` after establishing SSH. The agent sets it via `NewLocal` at startup. No fallback, no branching.

### Credential resolution

**`CredentialSource`** (`pkg/provider/resolve.go`) abstracts where credentials come from:
- `EnvSource` — `os.Getenv` (CLI, no secrets provider)
- `SecretsSource` — external secrets provider (Infisical, Doppler, AWS SM)
- `MapSource` — in-memory map (tests)

**Single source.** `DeployContext.Creds` holds the `CredentialSource`. Set once at the boundary (`cmd/`). The reconcile loop reads from it via `Secrets()`. All providers, database creds, and app secrets go through the same source.

**Contract:** `Get(key) (string, error)` returns `("", nil)` for absent keys. Errors are for real failures (auth, network). Enforcement happens later — `Secrets()` hard-errors on missing global secrets.

### Reconcile flow

```
Deploy(ctx, dc, cfg)
  → ValidateConfig(cfg)
  → cfg.Resolve()
  → DescribeLive(ctx, dc, cfg) → LiveState
  → ServersAdd(ctx, dc, cfg)           — INFRA: create desired servers
  → establish MasterSSH + KubeClient   — bridge: SSH tunnel → client-go
  → Firewall(ctx, dc, live, cfg)       — INFRA: provider API
  → Volumes(ctx, dc, live, cfg)        — INFRA: provider API + SSH mount
  → Build(ctx, dc, cfg)                — INFRA: docker build
  → Secrets(ctx, dc, live, cfg)        — RUNTIME: CredentialSource → values
  → packages.ReconcileAll(ctx, dc, cfg) — RUNTIME: k8s via KubeClient
  → Storage(ctx, dc, live, cfg)        — INFRA: provider API
  → Services(ctx, dc, live, cfg, sources) — RUNTIME: k8s via KubeClient
  → Crons(ctx, dc, live, cfg, sources) — RUNTIME: k8s via KubeClient
  → ServersRemoveOrphans(ctx, dc, live, cfg) — MIXED: drain (KubeClient) + delete (provider)
  → DNS(ctx, dc, live, cfg)            — INFRA: provider API
  → Ingress(ctx, dc, live, cfg)        — RUNTIME: k8s via KubeClient
```

Infra steps use SSH + provider APIs. Runtime steps use KubeClient. Files prefixed `infra_` / `runtime_` in `internal/reconcile/`.

### Database package

`database:` in config triggers the database package. Per database:
1. Detect engine from image (postgres, mysql)
2. Read credentials from `DeployContext.DatabaseCreds` (required, no auto-generation)
3. Store as k8s Secret via KubeClient
4. Apply StatefulSet + headless Service via KubeClient
5. Wait for readiness via KubeClient.WaitRolloutReady
6. Create backup bucket (provider API)
7. Store backup bucket creds via KubeClient
8. Apply backup CronJob via KubeClient
9. Return env vars as `$VAR` resolution sources for downstream services

### Server provisioning

`ComputeSet` flow per server:
1. `EnsureServer` at provider (create or return existing)
2. Resolve private IP
3. Clear stale known host (recycled IPs)
4. Wait for SSH
5. `EnsureSwap`, `EnsureDocker`
6. Master: `InstallK3sMaster` + `EnsureRegistry` + `InstallAgent`
7. Worker: `JoinK3sWorker`
8. `LabelNode` (SSH kubectl — bootstrap only, before KubeClient exists)

After first deploy: agent installed on master via systemd. Subsequent deploys go through agent.

## Providers

| Kind | YAML key | Interface | Implementations |
|------|----------|-----------|----------------|
| Compute | `providers.compute` | `ComputeProvider` | hetzner, aws, scaleway |
| DNS | `providers.dns` | `DNSProvider` | cloudflare, aws, scaleway |
| Storage | `providers.storage` | `BucketProvider` | cloudflare (R2), aws (S3), scaleway |
| Build | `providers.build` | `BuildProvider` | local, daytona, github |
| Secrets | `providers.secrets` | `SecretsProvider` | doppler, awssm, infisical |

## Working tree

The working tree frequently has uncommitted changes — that's normal. The on-disk file is always the intended version. Commits happen when the user asks.

## Key rules

1. `app` + `env` in `nvoi.yaml` are required. They're the namespace for everything.
2. No state files. Infrastructure is the truth.
3. `deploy` is idempotent. Run twice, same result.
4. `teardown` nukes external provider resources. Volumes and storage preserved by default.
5. Provider interfaces scale. Add a provider = implement the interface.
6. Naming: `nvoi-{app}-{env}-{resource}`. Deterministic. No UUIDs.
7. **`os.Getenv` is never called directly except through `EnvSource`, which is selected at the `cmd/` boundary.** `internal/`, `pkg/` never decide to read env vars — they receive a `CredentialSource` from `cmd/`. `cmd/cli/agent.go` resolves env values, passes them to the agent via `AgentOpts`.
8. **Providers are silent.** Never print or narrate. Output via `pkg/core/` → `Output` interface.
9. **`pkg/core/` never writes to stdout.** All output through `Output` interface.
10. **Every provider operation goes through `pkg/core/`.** No direct provider calls.
11. **`pkg/core/` never imports `net/http`.** HTTP calls belong in `infra/` or `provider/`.
12. **Errors flow up, render once.** `pkg/core/` returns errors. Cobra renders via `Output.Error()`.
13. **No shell injection.** Secret values via KubeClient typed API, not inline interpolation.
14. **Web-facing services require replicas >= 2.** Omitted defaults to 2, explicit 1 is a hard error.
15. **Package-managed resources are protected from orphan detection.**
16. **Database credentials are user-owned.** No auto-generation. Missing = hard error.
17. **Input validated once at the boundary.** `ValidateConfig` is the only validation point.
18. **Agent JSONL-only.** Every agent endpoint returns JSONL. Handlers receive `*jsonlOutput`, not `http.ResponseWriter`. Enforced by `CommandFunc` type signature.
19. **KubeClient always set.** `Cluster.Kube` is non-nil before any k8s operation. No fallback to SSH kubectl. No branching.
20. **One kubectl path: `kctl()`.** Used only by `LabelNode` during bootstrap (before KubeClient exists). Everything else goes through KubeClient.
21. **Async provider actions polled to completion.** Fire-and-forget = production race condition.
22. **`DeleteServer` detaches firewall before termination.**
23. **SecretsProvider is read-only.** `Get()` and `List()` only. nvoi never writes secrets. `Get()` returns `("", nil)` for absent keys — errors are for real failures.

## Production hardening notes

- **Ingress uses k3s built-in Traefik.** Standard k8s Ingress resources, one per service.
- **DNS and ingress are separate concerns.** DNS creates A records. Ingress creates k8s Ingress resources.
- **SSH host key changed = hard error** with guidance to clear known hosts. Auto-cleared on server creation.
- **Firewall never reset during server creation.** `ensureFirewall` only ensures existence.
- **Concurrency control on deploy workflows.** `concurrency: { group: deploy, cancel-in-progress: false }`.
- **Root disk size is creation-only.** Resize requires server recreation.
- **Pod eviction errors propagated during drain.** Failed eviction on a Ready node blocks server deletion.
- **Agent credentials on master.** The agent model puts provider credentials on the master's disk (`.env` in the agent working directory, mode 0600, owned by deploy). This is a trade-off vs the SSH model where creds never left the laptop. When `providers.secrets` is configured, only the secrets provider bootstrap creds need to be on disk — everything else is fetched at runtime via CredentialSource. Without a secrets provider, the full `.env` is required. Configuring a secrets provider minimizes the blast radius of a compromised master.
- **Agent auth.** Bearer token generated at install time, stored at `{agentDir}/agent.token` (mode 0600). All endpoints except `/health` require it. Backwards-compatible: agents installed before token auth accept all requests (empty token = no check). The agent hard-rejects binding to `0.0.0.0` — localhost only, SSH tunnel provides access.
