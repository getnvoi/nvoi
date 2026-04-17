# CLAUDE.md — nvoi

## What nvoi is

The foundational engine for reconciling cloud infrastructure + Kubernetes workloads from a single YAML. `nvoi deploy` converges live state to match the config. `nvoi teardown` nukes the provider side. `nvoi describe` reads the cluster live. Packages, databases, build pipelines and anything product-shaped live in an upper layer that consumes this engine — they are explicitly not part of core.

## Philosophy

- **`app` + `env` is the namespace.** Defined in `nvoi.yaml`. `nvoi-{app}-{env}-*`. Different app or env = brand new infrastructure.
- **No state files.** No manifest, no local cache. Infrastructure is the source of truth.
- **Everything is idempotent.** `nvoi deploy` reconciles: adds desired resources, removes orphans. Run twice, same result.
- **Naming is the lookup key.** `nvoi-{app}-{env}-{resource}`. Deterministic. No UUIDs. The naming convention finds everything.
- **Reconcile vs teardown.** Reconcile converges on a diff: queries live state, adds what's missing, removes what's orphaned. Teardown is a hard nuke: no diff, no live state query, wipes external provider resources. K8s resources die with the servers. Volumes and storage preserved by default — `--delete-volumes` / `--delete-storage` to nuke.
- **Declarative config, imperative reconciliation.** `nvoi.yaml` declares desired state. The reconciler walks each resource type in order.
- **Two-layer core.** Layer 1: provider infra (servers, firewall, volumes, DNS, buckets). Layer 2: k8s manifests (services, crons, ingress, secrets). Bound by deterministic naming. Nothing else.
- **Provider interfaces scale.** Hetzner, Cloudflare, AWS, Scaleway. Interface-first. Add a provider = implement the interface. Organized by domain: `compute/`, `dns/`, `storage/`.
- **SSH transport + typed kube client.** Single SSH connection per deploy (`MasterSSH`). A client-go `*kube.Client` tunnels through the same SSH to the apiserver (`MasterKube`), shared across all reconcile operations.
- **Secrets are k8s secrets.** Values live in the cluster only. Resolved at deploy time from env vars via `CredentialSource`. No opinionated external secret backends in core.

## Build & Test

```bash
bin/test                   # enforced per-package timeout — MUST pass, no exceptions
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

# Cron (k8s sugar)
bin/nvoi cron run cleanup      # trigger cron job immediately

# Inspect
bin/nvoi resources             # list all provider resources
go test ./...                  # run tests
```

### Files

| File | Purpose |
|------|---------|
| `nvoi.yaml` | Infrastructure config (tracked) |
| `.env` | Provider credentials + app secrets (not tracked) |
| `bin/nvoi` | Universal entrypoint — sources .env, builds `cmd/cli` |
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

servers:
  master:
    type: cax11
    region: nbg1
    role: master
    disk: 50                # root disk GB (optional, AWS + Scaleway only)

firewall: default           # string or list of port:cidr rules

volumes:
  pgdata:
    size: 20
    server: master

secrets:                    # user secrets, resolved from env vars
  - JWT_SECRET
  - ENCRYPTION_KEY

storage:
  assets: {}

services:
  api:
    image: myorg/api:v1
    port: 8080
    secrets: [JWT_SECRET, ENCRYPTION_KEY]
    server: master          # single node — nodeSelector
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

`ValidateConfig()` runs before touching infrastructure:

- `app` and `env` required
- `providers.compute` required
- At least one server, exactly one master, all have type/region/role. `disk` optional (creation-only, not resizable). Hetzner + `disk` = hard error (fixed per server type).
- Volumes: size > 0, server exists
- Services/crons: `image` required, referenced `storage`/`volumes` exist
- Volume mounts: `name:/path` format, volume must be on same server as workload
- `server` and `servers` mutually exclusive. Multiple servers + volume = error.
- Web-facing services (with domains): replicas omitted → defaults to 2. Explicit `replicas: 1` → hard error. 2 replicas on a single `server:` node is valid — the rule ensures process-level redundancy, not node distribution.

## Commands

```bash
nvoi deploy                              # reconcile to match config
nvoi teardown                            # nuke external provider resources
nvoi teardown --delete-volumes --delete-storage
nvoi describe                            # live cluster state
nvoi resources                           # list all provider resources
nvoi logs <service>                      # stream service logs
nvoi logs <service> -f                   # follow logs
nvoi exec <service> -- cmd               # run command in service pod
nvoi ssh -- cmd                          # run command on master node
nvoi cron run <name>                     # trigger cron job immediately
```

Global flags: `--config` (default: `nvoi.yaml`), `--json` (JSONL output), `--ci` (plain text).

nvoi is a local-first engine. Credentials live in your environment (`.env` or exported vars) and never leave your machine. There is no server, no account, no custody.

## Architecture

```
cmd/
  cli/                     CLI entrypoint — one file per command
    main.go                rootCmd, runtime wiring, PersistentPreRunE
    context.go             Env-source credential resolution (cmd/ boundary for os.Getenv)
    deploy.go..ssh.go      One file per command, dispatch to pkg/core / internal/*
  distribution/main.go     Binary distribution server (R2-backed)

internal/
  config/                  Shared types — no logic
    config.go              AppConfig, DeployContext, LiveState, definition types
  reconcile/               Deploy orchestrator — YAML to infrastructure
    reconcile.go           Deploy() — ordered reconciliation
    validate.go            ValidateConfig() — fail-fast pre-flight checks
    helpers.go             DescribeLive(), SplitServers(), ResolveServers()
    servers.go             ServersAdd (create) + ServersRemoveOrphans (drain + delete after services move)
    firewall.go            Firewall (desired per-role set) + FirewallRemoveOrphans (sweep, runs after ServersRemoveOrphans)
    volumes.go             Volume reconciliation
    secrets.go             Secret resolution — reads from CredentialSource
    storage.go             Storage reconciliation
    services.go            Service reconciliation (defaults replicas for domains)
    crons.go               Cron reconciliation
    dns.go                 DNS reconciliation
    ingress.go             Caddy reconcile (EnsureCaddy + admin-API hot-reload + per-domain cert/HTTPS waits)
    envvars.go             $VAR resolution
  core/                    Source-agnostic logic
    teardown.go            Teardown() — ordered resource deletion
  render/                  Output renderers — TUI, Plain, JSON
  testutil/                MockSSH, MockOutput, HetznerFake, CloudflareFake (see providermocks.go)
    kubefake/              KubeFake — *kube.Client over the typed fake clientset, with SetExec() for pod-shell mocking

pkg/
  core/                    Business logic. One file per domain. No cobra, no I/O, no stdout.
    cluster.go             Cluster struct (MasterSSH + MasterKube), ProviderRef, Connect(), SSH(), Kube()
    compute.go             ComputeSet (SSH connect, EnsureSwap, Docker, k3s, label), ComputeDelete, ComputeList
    service.go             ServiceSet, ServiceDelete
    dns.go                 DNSSet, DNSDelete, DNSList
    storage.go             StorageSet, StorageDelete, StorageEmpty, StorageList
    secret.go              SecretList, SecretReveal
    volume.go              VolumeSet, VolumeDelete, VolumeList
    cron.go                CronSet, CronDelete, CronRun
    describe.go            Describe, DescribeJSON (ingress sourced from Caddy admin API via GetCaddyRoutes)
    resources.go           Resources
    firewall.go            FirewallSet, FirewallList
    wait.go                WaitRollout
    exec.go                Exec
    ssh.go                 SSH
    logs.go                Logs
  kube/                    Typed Kubernetes client over SSH tunnel
    client.go              Client: typed clientset + SSH-tunneled rest.Config + ExecFunc hook; NewForTest(cs) for fakes
    apply.go               Apply() — typed create/update only (no dynamic / SSA fallback); EnsureNamespace
    workloads.go           FirstPod, GetServicePort, DeleteByName
    secrets.go             EnsureSecret, UpsertSecretKey, DeleteSecretKey, DeleteSecret, ListSecretKeys, GetSecretValue
    nodes.go               LabelNode, DrainAndRemoveNode (Eviction API, force-remove on NotReady)
    pods.go                GetAllPods
    cron.go                BuildCronJob, CreateJobFromCronJob, WaitForJob, DeleteCronByName
    caddy.go               EnsureCaddy, ReloadCaddyConfig, WaitForCaddyCert, WaitForCaddyHTTPS, GetCaddyRoutes
    caddy_config.go        BuildCaddyConfig — pure JSON renderer (deterministic, sorted by Service)
    caddy_manifests.go     buildCaddyDeployment / Service / ConfigMap / PVC + constants
    generate.go            BuildService (Deployment/StatefulSet + Service), ParseSecretRef
    rollout.go             WaitRollout (poll-based with terminal failure detection), RecentLogs
    diagnostics.go         timeoutDiagnostics, recentEvents (for rollout timeout error messages)
    streaming.go           StreamLogs, Exec (SPDY) — overridable via Client.ExecFunc for tests
  infra/                   SSH, server bootstrap, k3s, Docker, swap, volume mounting
  provider/                Provider interfaces + per-domain implementations
    compute.go             ComputeProvider interface
    dns.go                 DNSProvider interface
    bucket.go              BucketProvider interface
    resolve.go             CredentialSource, EnvSource, MapSource, registration, credential schemas
    s3ops/                 Shared S3 operations (CORS, lifecycle, empty)
    compute/               See pkg/provider/compute/CLAUDE.md for DeleteServer contract
      hetzner/             Hetzner Cloud (compute + volumes)
      aws/                 AWS (EC2 + VPC)
      scaleway/            Scaleway (compute)
    dns/
      cloudflare/          Cloudflare DNS
      aws/                 AWS Route53
      scaleway/            Scaleway DNS
    storage/
      cloudflare/          Cloudflare R2
      aws/                 AWS S3
      scaleway/            Scaleway Object Storage
    hetznerbase/           Shared Hetzner HTTP client
    awsbase/               Shared AWS SDK config
    cfbase/                Shared Cloudflare HTTP client
    scwbase/               Shared Scaleway HTTP client
  utils/                   Pure utilities: naming, poll, httpclient, ssh keys, format, maps, params
    s3/                    S3-compatible operations with AWS Signature V4
```

### SSH + Kube model

**Interface → implementation → mock:**
- `utils.SSHClient` (`pkg/utils/ssh.go`) — the SSH interface. Every SSH consumer takes this.
- `infra.SSHClient` (`pkg/infra/ssh.go`) — the real implementation. Wraps `golang.org/x/crypto/ssh` with SFTP upload, TCP dial, persistent connection.
- `testutil.MockSSH` (`internal/testutil/mock_ssh.go`) — test mock. Canned responses by exact command or prefix match. Records all calls for assertions.
- `*kube.Client` (`pkg/kube/client.go`) — typed Kubernetes client. In production it tunnels to the apiserver over `utils.SSHClient`. `kube.NewForTest(cs)` wraps a client-go fake typed clientset for tests. `Client.ExecFunc` is an injectable hook so tests can capture pod-shell calls (Caddy admin API, cert/HTTPS waits) without an SPDY connection.
- `kubefake.KubeFake` (`internal/testutil/kubefake/kubefake.go`) — Client + the underlying typed fake clientset for reconcile-level assertions. `kf.SetExec(fn)` overrides the Exec hook.

**Connection lifecycle:** One SSH connection per deploy, one kube client per deploy. `Cluster.MasterSSH` + `Cluster.MasterKube` are set once after `ServersAdd()`, shared across all subsequent operations via `borrowedSSH`/no-op cleanup. CLI dispatch path connects on-demand (no MasterSSH/MasterKube) and closes after.

**Testing:** Set `Cluster.MasterKube = kf.Client` to inject a fake kube client. For reconcile tests use `convergeDC(log, convergeMock())` — it wires both a MockSSH and a KubeFake and registers the fake in `kubeFakes` so tests can assert via `kfFor(dc)`. See `internal/reconcile/helpers_test.go` and `internal/reconcile/services_test.go` for patterns.

`ComputeSet` connects to individual servers via `Cluster.Connect()` for provisioning (Docker, k3s, swap). Those are separate connections — not the master.

SSH errors: `ErrHostKeyChanged` and `ErrAuthFailed` surface immediately with guidance. Stale known hosts auto-cleared on server creation.

### Reconcile flow

```
Deploy(ctx, dc, cfg)
  → ValidateConfig(cfg)
  → cfg.Resolve()                    — populate VolumeDef.MountPath, firewall names
  → DescribeLive(ctx, dc, cfg) → LiveState
  → ServersAdd(ctx, dc, cfg)          — create desired, NO orphan removal yet
  → establish MasterSSH + MasterKube
  → Firewall(ctx, dc, live, cfg)      — desired per-role set, NO orphan removal yet
  → Volumes(ctx, dc, live, cfg)
  → Secrets(ctx, dc, live, cfg) → secretValues
  → Storage(ctx, dc, live, cfg) → storageCreds
  → mergeSources(secretValues, storageCreds) → sources
  → Services(ctx, dc, live, cfg, sources)
  → Crons(ctx, dc, live, cfg, sources)
  → ServersRemoveOrphans(ctx, dc, live, cfg) — drain + delete AFTER workloads moved
  → FirewallRemoveOrphans(ctx, dc, live, cfg) — sweep AFTER servers detach; Hetzner refuses delete while attached
  → DNS(ctx, dc, live, cfg)
  → Ingress(ctx, dc, live, cfg)
```

### Server provisioning

`ComputeSet` flow per server:
1. `EnsureServer` at provider (create or return existing)
2. Resolve private IP
3. Clear stale known host (recycled IPs)
4. Wait for SSH (poll `Connect`, hard error on host key changed / auth failed)
5. `EnsureSwap` — reads actual disk size via `df`, proportional swap (5%, 512MB–2GB)
6. `EnsureDocker`
7. Master: `InstallK3sMaster` + `EnsureRegistry`
8. Worker: `JoinK3sWorker` (reads token from master, installs agent)
9. `LabelNode` via kube client

Zero-downtime server replacement: `ServersAdd` creates new servers first, `Services`/`Crons` move workloads, `ServersRemoveOrphans` drains and deletes old servers after.

## Providers

Organized by domain with shared base clients:

| Kind | YAML key | Interface | Implementations |
|------|----------|-----------|----------------|
| Compute | `providers.compute` | `ComputeProvider` | hetzner, aws, scaleway |
| DNS | `providers.dns` | `DNSProvider` | cloudflare, aws, scaleway |
| Storage | `providers.storage` | `BucketProvider` | cloudflare (R2), aws (S3), scaleway |

`ensureFirewall` only ensures the resource exists — never resets rules. Rules managed exclusively by `ReconcileFirewallRules` in the Firewall reconcile step.

## Credential resolution

Every credential — provider tokens, SSH key, secret values referenced via `$VAR` — goes through a single `provider.CredentialSource` built at the `cmd/` boundary in `cmd/cli/context.go`.

**`CredentialSource = EnvSource{}`.** `source.Get(k)` is literally `os.Getenv(k)`. All referenced variables come from the shell/`.env`.

| Credential | Resolution |
|---|---|
| Compute / DNS / Storage creds | env (schema `EnvVar` field) |
| Service `$VAR` in `secrets:` | env |
| SSH private key | `SSH_PRIVATE_KEY` env → `SSH_KEY_PATH` env → `~/.ssh/id_ed25519` → `~/.ssh/id_rsa` |

Opinionated external secret backends (Doppler, AWS Secrets Manager, Infisical) are product-layer concerns. Core exposes `CredentialSource` as the extension point; implementations plug in outside this repo.

## Working tree

The working tree frequently has uncommitted changes — that's normal. The on-disk file is always the intended version. When reviewing, never flag a mismatch between a prior commit and the working tree as a bug. The working tree is the source of truth. Commits happen when the user asks.

## Key rules

1. `app` + `env` in `nvoi.yaml` are required. They're the namespace for everything.
2. No state files. Infrastructure is the truth.
3. `deploy` is idempotent. Run twice, same result.
4. `teardown` nukes external provider resources. Volumes and storage preserved by default.
5. Provider interfaces scale. Add a provider = implement the interface.
6. Naming: `nvoi-{app}-{env}-{resource}`. Deterministic. No UUIDs.
7. SSH keys injected via cloud-init only. Single SSH connection per deploy (`MasterSSH`), single kube client (`MasterKube`).
8. **`os.Getenv` lives exclusively in `cmd/`.** `internal/`, `pkg/` never read env vars. `cmd/cli/context.go` builds an `EnvSource` at the boundary. Everything downstream calls `source.Get(key)`; nothing else touches env.
9. **Providers are silent.** Never print or narrate. Output via `pkg/core/` → `Output` interface.
10. **`pkg/core/` never writes to stdout.** All output through `Output` interface.
11. **Every provider operation goes through `pkg/core/`.** No caller invokes a provider method directly. `pkg/core/` wraps every operation with output, error handling, and naming resolution.
12. **`pkg/core/` never imports `net/http`.** HTTP calls belong in `infra/` or `provider/`.
13. **Errors flow up, render once.** `pkg/core/` returns errors. Cobra renders via `Output.Error()`.
14. **No shell injection.** Secrets flow through the typed kube client — no shell interpolation of user data.
15. **Web-facing services require replicas >= 2.** Omitted defaults to 2, explicit 1 is a hard error.
16. **Input validated once at the boundary.** Config parse (`ValidateConfig`) is the only place that validates user input. Internal code trusts validated input — no defensive escaping, no silent sanitization. Validators: `ValidateName` (DNS-1123) for resource names, `ValidateEnvVarName` (POSIX) for secret keys, `ValidateDomain` for domains. All in `pkg/utils/naming.go`.
17. **Single binary, one mode.** `cmd/cli` reads `nvoi.yaml`, resolves credentials from env vars, and calls `internal/reconcile` / `pkg/core/` directly. No server, no relay, no custody.
18. **Every kube op goes through `*kube.Client`.** Exported methods on `Client` are the only way `internal/reconcile` or `pkg/core/` talk to the apiserver. No inline kubectl strings, no direct client-go usage outside `pkg/kube/`.
19. **Async provider actions polled to completion.** Every action that returns an ID must be polled via `waitForAction` before proceeding. Fire-and-forget = production race condition.
20. **`DeleteServer` detaches firewall before termination.** Every provider. Hetzner: `detachFirewall` + poll. AWS: move to VPC default SG. Scaleway: reassign to project default SG. `DeleteFirewall` retries "still in use."

## Production hardening notes

- **`~` doesn't expand in Go.** `resolveSSHKey()` handles tilde expansion against `$HOME`.
- **`Client.Apply` is typed-only.** Every kind we ship is dispatched through the typed clientset (Get → Create-if-missing → Update-otherwise) with `FieldManager: "nvoi"`. There is no dynamic / SSA fallback — unknown kinds error out. Add a case to `applyTyped` if you need a new resource type. For Deployment/StatefulSet, `Apply` preserves `.status` from the existing object so re-running a Ready workload doesn't reset its readiness in tests (mirrors real apiserver semantics where status has its own subresource).
- **Ingress is in-cluster Caddy.** k3s installed with `--disable traefik --disable servicelb`. A single `caddy:2.10-alpine` Deployment runs in `kube-system` with `nodeSelector: nvoi-role=master` and `hostPort: 80/443`. The reconciler talks to Caddy's admin API on `localhost:2019` *inside the pod* via `kube.Client.Exec` — admin is never exposed off-pod. Each reconcile builds Caddy's native JSON config from `cfg.Domains` + resolved Service ports (`pkg/kube/caddy_config.go`) and POSTs it to `/load`; Caddy validates first, then atomically swaps listeners with no connection drops. ACME state lives on a 1Gi PVC at `/data` (k3s `local-path`). Removed domains drop out of the next config; orphan ingress resources don't exist (no k8s `Ingress` objects).
- **DNS and ingress are separate concerns.** DNS creates A records. Ingress is purely Caddy's loaded config.
- **HTTPS verification is two-step, both probes inside the Caddy pod.** Step 1: `WaitForCaddyCert` polls until `/data/caddy/certificates/acme-v02.api.letsencrypt.org-directory/<domain>/<domain>.crt` exists with non-zero size. Step 2: `WaitForCaddyHTTPS` curls `https://<domain><health>` and waits for any non-5xx, non-0 status. Both run via `Exec` so we don't depend on the operator's local DNS. Timeouts are `caddyCertTimeout` / `caddyHTTPSTimeout` — expiration warns and continues (Caddy keeps retrying ACME, next deploy re-verifies).
- **SSH host key changed = hard error** with guidance to clear known hosts. Auto-cleared on server creation.
- **Firewall never reset during server creation.** `ensureFirewall` only ensures existence.
- **Firewall orphan sweep deferred until after `ServersRemoveOrphans`.** `Firewall()` reconciles the desired per-role set (master-fw, worker-fw) early so new servers get rules applied before workloads land. `FirewallRemoveOrphans()` runs later, after `ServersRemoveOrphans` has drained + deleted orphan servers (and `DeleteServer`'s contract has detached their firewalls). Running the sweep earlier — as the code used to — meant Hetzner correctly rejected `DeleteFirewall` with `resource_in_use` because the orphan server was still attached, and nothing retried. Same split pattern as `ServersAdd` / `ServersRemoveOrphans`. Firewall is the only reconcile-swept resource with a delete-time attachment lock against another reconcile-managed resource (servers); every other resource either has no lock or isn't swept in reconcile.
- **Concurrency control on deploy workflows.** `concurrency: { group: deploy, cancel-in-progress: false }`.
- **Root disk size is creation-only.** `disk` in server config applies at `EnsureServer` time. Changing it on an existing server has no effect — `EnsureServer` returns the existing server as-is. Resize requires server recreation. Hetzner doesn't support custom root disk sizes at all (fixed per server type) — validated at config time.

## Testing — mock governance

**One pattern. One file. No class-rewrite mocks.** Every test in this repo obeys the following rules. Violations are reverted in review.

### The only boundaries we mock

| Boundary | Mock | Rationale |
|---|---|---|
| HTTP to cloud provider APIs (Hetzner / Cloudflare / AWS / Scaleway) | `internal/testutil/providermocks.go` — `HetznerFake`, `CloudflareFake` | Real wire protocol + real provider client. No in-process reimplementation of provider semantics. |
| SSH protocol to provisioned servers | `internal/testutil/mock_ssh.go` — `MockSSH` | Real SSH command/upload protocol, canned prefix responses. |
| Kubernetes apiserver | `internal/testutil/kubefake/kubefake.go` — `KubeFake` | Wraps client-go's own fake clientset. |

Nothing else is mocked. `MockOutput` in `mock_provider.go` is NOT a boundary mock — it's an internal UI-event capture for assertions.

### Rules — hard

1. **One file for provider mocks.** `internal/testutil/providermocks.go`. Anything provider-mock-shaped lives here. Violation = reverted in review.

2. **No test declares a provider-interface type.** No test file contains `type myFooMock struct { ... }` that implements `provider.ComputeProvider` / `DNSProvider` / `BucketProvider`. Ever. The real provider clients are exercised end-to-end against the httptest-backed fakes.

3. **Tests seed state, never stub behavior.** `fake.SeedServer(...)` / `SeedVolume` / `SeedFirewall` / `SeedNetwork` / `SeedDNSRecord` / `SeedBucket`. No `func(req) error` hooks on mocks.

4. **The `OpLog` is the assertion surface.** Tests call `fake.Has("ensure-server:X")` / `Count("delete-server:")` / `IndexOf` / `All`. New ops = one handler edit in `providermocks.go`, nothing added test-side.

5. **Error injection is explicit.** `fake.ErrorOn("delete-firewall:nvoi-myapp-prod-master-fw", err)`. The matching HTTP handler short-circuits to 500. No `if testMode then ...` inside production code.

6. **Registration is per-test.** `fake := testutil.NewHetznerFake(t); fake.Register("test-name")`. Re-registration replaces the previous factory. No init-time globals except the shared "test-compute" / "cluster-test" / "cron-test" / "test" defaults that a fresh `convergeDC` re-registers under `test-reconcile` for each test.

7. **`testutil.MockCompute`, `testutil.MockDNS`, `testutil.MockBucket` are gone.** Not deprecated, deleted. Any PR reintroducing a provider-interface-satisfying mock type — in any file — fails review. If you feel you need one, extend `providermocks.go`.

8. **`MockSSH` and `KubeFake` stay as-is.** They're at correct boundaries.

9. **`MockOutput` stays.** Not an external boundary — it's an internal contract. The capture-event pattern is legitimate.

### What this buys

- Adding a new test op (e.g. rate-limit path, 5xx retry) = one branch in one handler, not a new mock type.
- Refactoring any provider interface = zero test-side work. Tests don't reference the interface.
- Adding a new cloud provider for compute / DNS / storage = add new httptest handlers in `providermocks.go` + a `Register()` method. No `testutil.MockX` surface to maintain.
- "Mock passes because mock matches itself" is structurally impossible — the fake speaks the real wire, and the real client decodes it.

### If you're writing a new test

1. Need compute? `fake := testutil.NewHetznerFake(t); fake.Register("my-test-provider")`. Seed via `fake.SeedServer(...)` / `SeedVolume` / etc. Point the `Cluster.Provider` at `"my-test-provider"`.
2. Need DNS or R2? `cf := testutil.NewCloudflareFake(t, opts); cf.RegisterDNS("dns-name")` / `cf.RegisterBucket("bucket-name")`. Seed records and buckets as needed.
3. Need SSH? Use `testutil.MockSSH` with `Prefixes`.
4. Need kube? Use `kubefake.NewKubeFake()`.
5. Need to assert ops? `fake.Has("ensure-server:X")`, `fake.Count("delete-...")`, `fake.IndexOf(...)` for ordering.

Read the file-top comment in `providermocks.go` before extending it.
