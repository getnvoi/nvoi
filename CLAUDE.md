# CLAUDE.md — nvoi

## What nvoi is

The foundational engine for reconciling cloud infrastructure + Kubernetes workloads from a single YAML. `nvoi deploy` converges live state to match the config. `nvoi teardown` nukes the provider side. `nvoi describe` reads the cluster live. Packages, build pipelines and other product-layer concerns stay outside core; database lifecycle is the explicit exception and lands in core via the `databases:` provider surface.

## Philosophy

- **`app` + `env` is the namespace.** `nvoi-{app}-{env}-*`. Different app or env = brand new infrastructure.
- **No state files.** Infrastructure is the source of truth. No manifest, no local cache.
- **Naming is the lookup key.** `nvoi-{app}-{env}-{resource}`. Deterministic, no UUIDs — the naming convention finds everything.
- **Reconcile vs teardown.** Reconcile converges on a diff. Teardown is a hard nuke of external provider resources; k8s dies with the servers. Volumes and storage preserved by default (`--delete-volumes` / `--delete-storage` to nuke).
- **Two-layer core.** Layer 1: provider infra (servers, firewall, volumes, DNS, buckets, databases). Layer 2: k8s manifests (services, crons, ingress, secrets). Bound by deterministic naming.
- **InfraProvider owns its convergence — split contract: Connect (read-only) vs Bootstrap (writes).** Every infra backend (Hetzner / AWS / Scaleway today, Daytona / managed-k8s tomorrow) implements one interface (`pkg/provider/infra.go::InfraProvider`). `reconcile.Deploy` calls `infra.Bootstrap` (drift reconciled, missing resources created). Every other CLI command (`logs`, `exec`, `describe`, `cron run`, `resources`, `ssh`) routes through `Cluster.Kube` / `Cluster.SSH` to `infra.Connect` / `infra.NodeShell` — read-only attach, no provider mutations. Reconcile never branches on provider name.
- **SSH transport + typed kube client.** When the provider has a host shell (every IaaS), it returns one via `infra.NodeShell` — cached on `Cluster.NodeShell`. The kube client `infra.Bootstrap` (or `infra.Connect`) returns is cached on `Cluster.MasterKube`. Both shared across every reconcile step.
- **Credentials flow through one `CredentialSource`.** Default `EnvSource` reads `os.Getenv`. When `providers.secrets` is set to `doppler | awssm | infisical`, source switches to `SecretsSource` and every credential (infra, DNS, storage, SSH key, service `$VAR`) fetches from the backend's direct API — no shell-outs.

## Build & Test

```bash
bin/test                   # enforced per-package timeout — MUST pass
bin/test -v                # verbose
go test ./... -cover       # coverage
go build ./cmd/cli
```

**Test suite MUST complete in under 2 seconds per package.** `bin/test` enforces this with `go test -timeout 2s`. Any test exceeding it is broken — fix by mocking I/O waits (SSH polls, HTTP retries, stability delays). Never sleep in tests. Override production timeouts with `kube.SetTestTiming(time.Millisecond, time.Millisecond)` in test `init()`. Non-negotiable.

## CI

GitHub Actions workflows (`.github/workflows/`):

- **ci.yml** — fmt + vet + test + build on push and PR
- **deploy.yml** — production deploy on push to main (runs `bin/deploy`)
- **release.yml** — cross-compile on git tags (`v*`), upload to R2

**PR merges:** Never squash. `gh pr merge --merge --delete-branch`.

## Local development

`bin/nvoi` is the universal entrypoint — sources `.env`, builds the binary if needed, runs any command.

```bash
bin/nvoi deploy | teardown | describe | resources
bin/nvoi logs <service> [-f]
bin/nvoi exec <service> -- sh
bin/nvoi ssh -- <cmd>
bin/nvoi cron run <name>
bin/deploy                     # shorthand for bin/nvoi deploy
bin/destroy                    # shorthand for bin/nvoi teardown
```

Global flags: `--config` (default: `nvoi.yaml`), `--json` (JSONL output), `--ci` (plain text).

### Files

| File | Purpose |
|------|---------|
| `nvoi.yaml` | Infrastructure config (tracked) |
| `.env` | Provider credentials + app secrets (not tracked) |
| `bin/nvoi` | Universal entrypoint — sources `.env`, builds `cmd/cli` |
| `bin/deploy` | Shorthand for `bin/nvoi deploy` |
| `bin/destroy` | Shorthand for `bin/nvoi teardown` |

nvoi is a local-first engine. Credentials live in your environment (`.env` or exported vars) and never leave your machine. No server, no account, no custody.

## Config format

```yaml
app: myapp
env: production

providers:
  infra: hetzner            # hetzner | aws | scaleway (was: providers.compute, removed in #47)
  dns: cloudflare           # cloudflare | aws | scaleway
  storage: cloudflare       # cloudflare | aws | scaleway
  secrets: infisical        # optional — doppler | awssm | infisical (see Credential resolution)
  tunnel: cloudflare        # optional — cloudflare | ngrok (see Tunnel ingress)
  ci: github                # optional — github (only consumed by `nvoi ci init`; see CI onboarding)
  build: local              # optional — local (default) | ssh | daytona

servers:
  master:
    type: cax11
    region: nbg1
    role: master
    disk: 50                # root disk GB — optional, AWS + Scaleway only (creation-time)

firewall: default           # string or list of port:cidr rules

volumes:
  pgdata:
    size: 20
    server: master

secrets:                    # resolved via CredentialSource (env or backend)
  - JWT_SECRET
  - ENCRYPTION_KEY

registry:                   # private container-registry pull creds (optional)
  ghcr.io:                  # <host>[:port]
    username: $GITHUB_USER
    password: $GITHUB_TOKEN

storage:
  assets: {}

databases:
  app:
    engine: postgres
    version: "16"
    server: master
    size: 20
    user: $POSTGRES_APP_USER
    password: $POSTGRES_APP_PASSWORD
    database: myapp

services:
  api:
    image: ghcr.io/myorg/api:v1
    build: ./services/api   # bool | string | {context, dockerfile} — requires registry: entry for the image's host
    port: 8080
    secrets: [JWT_SECRET, ENCRYPTION_KEY]
    databases: [app]        # injects DATABASE_URL_APP
    server: master          # nodeSelector
    # servers: [worker-1, worker-2]  # nodeAffinity + topologySpread

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

- `app` and `env` required; `providers.infra` required (the legacy `providers.compute` key was removed in #47 — use `providers.infra`).
- At least one server, exactly one master, all have type/region/role. `disk` optional (creation-only, not resizable). Hetzner + `disk` = hard error (fixed per server type).
- Volumes: size > 0, server exists. `server` / `servers` mutually exclusive. Multiple servers + volume = error.
- Databases: `engine` required and must be a registered database provider. Selfhosted engines require `user` / `password` / `database` / `server` / `size`; SaaS engines reject those fields. `backup.storage` must reference `storage:`.
- Services/crons: `image` required (unless `build:`), referenced `storage`/`volumes` exist. Volume mounts `name:/path`, volume must be on the same server as the workload.
- Web-facing services (with domains): replicas omitted → 2. Explicit `replicas: 1` → hard error. 2 replicas on a single `server:` node is valid.
- `services.X.build:` requires a `registry:` block AND the image's host must appear as a key. Bare image names (`nginx`, `alpine:3.19`) are rejected for built services — push needs a fully qualified tag.
- `providers.tunnel` — optional. When set: `providers.dns` required, at least one `domains:` entry required. Valid values: `cloudflare | ngrok`. Credentials validated at startup.
- `providers.ci` — optional. When set, must be a registered CI provider (today: `github`). Pure metadata consumed ONLY by `nvoi ci init`; `reconcile.Deploy` never reads it. Unknown name = hard validate error.

## Private registries + local builds

Two independent concerns that compose:

- **Pull credentials** (`registry:` block) — how kubelet authenticates when pulling images. Rendered to a `kubernetes.io/dockerconfigjson` Secret named `registry-auth`; `imagePullSecrets: [{name: registry-auth}]` injected into every PodSpec when `len(cfg.Registry) > 0`. `$VAR` values resolved via `CredentialSource`. Removing the block deletes the orphan Secret on the next deploy.
- **Local builds** (`services.X.build:`) — `true` (`./`) | `"path"` | `{context, dockerfile}`. Runs PRE-infra via `pkg/core/build.go::BuildService` — build failure aborts before any server is provisioned. Passwords via `--password-stdin`, never argv. Runs against the operator's real `~/.docker/config.json` (Kamal-style; DOCKER_CONFIG isolation breaks plugin discovery and docker-context lookup).

**Image resolution (Kamal-style, multi-registry-aware):**

- `image: ghcr.io/org/api` — host explicit, must appear as a key in `registry:`.
- `image: org/api` with exactly ONE `registry:` entry — host inferred.
- `image: org/api` with multiple registries — validate error (ambiguous). Write the host explicitly.
- `image: nginx` (bare shortname) under `build:` — validate error.
- `image: repo@sha256:...` — passes through unmodified.

**Deploy hash tag + label:** `dc.Cluster.DeployHash = YYYYMMDD-HHMMSS` (UTC). User tag preserved and suffixed with the hash (`:v2` → `:v2-<hash>`); no user tag → `:<hash>`. Same hash stamped as `nvoi/deploy-hash: <hash>` on workload metadata AND pod-template metadata (NEVER selectors). Every deploy produces a new `image:` string so rolling updates always trigger without a `:latest` foot-gun.

Chmod / Dockerfile hygiene is the user's responsibility — nvoi does not mutate the build context. Use BuildKit `COPY --chmod=0755` or `RUN chmod +x` inside the Dockerfile.

## Architecture

```
cmd/
  cli/                     CLI entrypoint — one file per command
    main.go                rootCmd, runtime wiring, PersistentPreRunE
    context.go             Credential source resolution — boundary for os.Getenv
    deploy.go..ssh.go      One file per command, dispatch to pkg/core / internal/*
  distribution/main.go     Binary distribution server (R2-backed)

internal/
  config/                  Shared types — AppConfig, DeployContext, LiveState (no logic)
  reconcile/               Deploy orchestrator — see internal/reconcile/CLAUDE.md
  core/teardown.go         Teardown() — ordered resource deletion
  render/                  Output renderers — see internal/render/CLAUDE.md
  testutil/                MockSSH, MockOutput, HetznerFake, CloudflareFake (see providermocks.go)
    kubefake/              KubeFake — *kube.Client over typed fake clientset, with SetExec()

pkg/
  core/                    Business logic. One file per domain. No cobra, no I/O, no stdout.
    cluster.go             Cluster (Provider, Credentials, NodeShell, MasterKube, DeployHash); SSH() / Kube() borrow accessors
    service.go cron.go     ServiceSet / CronSet — kube workload appliers (KnownVolumes from caller)
    storage.go secret.go   StorageSet / Secret resolution (no provider lookup)
    describe.go resources.go logs.go exec.go ssh.go wait.go
    build.go               BuildService + BuildRunner interface (DockerRunner in prod, fake in tests)
  kube/                    Typed Kubernetes client over SSH tunnel
    client.go              Client: typed clientset + SSH-tunneled rest.Config + ExecFunc hook; NewForTest(cs)
    apply.go               Apply() — typed create/update only; every Get/Update wrapped in retry.RetryOnConflict
    workloads.go secrets.go nodes.go pods.go cron.go
    caddy.go               EnsureCaddy, ReloadCaddyConfig, WaitForCaddyCert, WaitForCaddyHTTPS, GetCaddyRoutes
    caddy_config.go        BuildCaddyConfig — pure JSON renderer (deterministic, sorted by Service)
    caddy_manifests.go     buildCaddyDeployment / Service / ConfigMap / PVC + constants
    tunnel.go              PurgeTunnelAgents, PurgeCaddy, GetTunnelAgentPods + well-known agent name constants
    registry.go            BuildDockerConfigJSON + BuildPullSecret for imagePullSecrets
    generate.go            BuildService (Deployment/StatefulSet + Service), ParseSecretRef
    rollout.go diagnostics.go streaming.go
  infra/                   SSH (golang.org/x/crypto/ssh + SFTP), k3s, swap, volume mounting
  provider/                Provider interfaces + per-domain implementations — see pkg/provider/CLAUDE.md
    infra.go               InfraProvider interface (Bootstrap → *kube.Client) + BootstrapContext + LiveSnapshot + IngressBinding
    dns.go                 DNSProvider — RouteTo / Unroute / ListBindings (polymorphic over A/AAAA/CNAME)
    bucket.go secrets.go   BucketProvider / SecretsProvider
    tunnel.go              TunnelProvider interface + TunnelRequest/Route/Plan + RegisterTunnel/ResolveTunnel
    types.go resolve.go    Server/Volume/Firewall/Network types + RegisterX/ResolveX registries
    hetzner/               infra (+ register)
    aws/                   infra + dns + storage (+ registers)
    scaleway/              infra + dns + storage (+ registers)
    cloudflare/            dns + storage + tunnel — tunnel.go, tunnel_client.go, workloads.go
    ngrok/                 tunnel — tunnel.go, client.go, workloads.go
    secrets/{doppler,awssm,infisical}/ — direct-API credential backends
  utils/                   Pure utilities: naming, poll, httpclient, ssh keys, format, maps, params
    s3/                    S3-compatible ops with AWS Signature V4
```

**Sub-CLAUDE.md references:**

- `internal/reconcile/CLAUDE.md` — reconcile flow, step notes, edge cases
- `internal/render/CLAUDE.md` — renderers (TUI / Plain / JSON)
- `pkg/provider/CLAUDE.md` — provider interface + registration pattern, credential resolution
- `pkg/provider/infra/CLAUDE.md` — InfraProvider impl pattern + DeleteServer contract shared across all backends

### SSH + Kube model

**Interface → implementation → mock:**
- `utils.SSHClient` (`pkg/utils/ssh.go`) — the SSH interface. Every consumer takes this.
- `infra.SSHClient` (`pkg/infra/ssh.go`) — real implementation. SFTP upload, TCP dial, persistent connection.
- `testutil.MockSSH` — canned prefix responses, records every call.
- `*kube.Client` (`pkg/kube/client.go`) — typed Kubernetes client. Tunnels through `utils.SSHClient` to the apiserver. `NewForTest(cs)` wraps client-go's typed fake clientset. `Client.ExecFunc` is an injectable hook so tests capture pod-shell calls (Caddy admin API, cert/HTTPS waits) without an SPDY connection.
- `kubefake.KubeFake` — Client + the typed fake for reconcile-level assertions. `kf.SetExec(fn)` overrides the Exec hook.

**Connection lifecycle:** One SSH + one kube client per command. The InfraProvider owns both, with two distinct entry points:
- `infra.Bootstrap(ctx, dc) → *kube.Client` (write) — provisions infra (servers + firewall + volumes + k3s install) and tail-calls `Connect`. `reconcile.Deploy` only.
- `infra.Connect(ctx, dc) → *kube.Client` (read-only) — looks up existing infra, dials SSH, builds the kube tunnel. Returns `provider.ErrNotBootstrapped` when nothing's there. `Cluster.Kube` (CLI dispatch path) calls this; cost ≤500ms on existing clusters.
- `infra.NodeShell(ctx, dc) → utils.SSHClient` returns the same SSH (cached) for `nvoi ssh`. Providers without a host shell return `(nil, nil)` and the CLI errors with an actionable message.
- `infra.Close()` releases the cached SSH at end of command.
- Reconcile stores both on `dc.Cluster.NodeShell` / `dc.Cluster.MasterKube` and shares them via `borrowedSSH` / no-op cleanup across every step.

**On-demand contract.** `Cluster.Kube(ctx, cfg)` and `Cluster.SSH(ctx, cfg)` route to `infra.Connect` / `infra.NodeShell` when their fields are nil. Tests NEVER pre-inject `Cluster.MasterKube` or `Cluster.NodeShell` — the on-demand path is mandatory coverage. CI gate: `grep -rE 'Cluster\.MasterKube\s*=|Cluster\.NodeShell\s*=' cmd/ pkg/core/ | grep '_test\.go'` returns zero hits.

SSH errors: `ErrHostKeyChanged` + `ErrAuthFailed` surface immediately with guidance. Stale known hosts auto-cleared on server creation.

## Providers

| Kind | YAML key | Interface | Implementations |
|------|----------|-----------|----------------|
| Infra | `providers.infra` | `InfraProvider` | hetzner, aws, scaleway (Daytona via #48) |
| DNS | `providers.dns` | `DNSProvider` | cloudflare, aws, scaleway |
| Storage | `providers.storage` | `BucketProvider` | cloudflare (R2), aws (S3), scaleway |
| Secrets | `providers.secrets` | `SecretsProvider` | doppler, awssm, infisical |
| Tunnel | `providers.tunnel` | `TunnelProvider` | cloudflare, ngrok |
| Build | `providers.build` | `BuildProvider` | local (default); ssh #56-B, daytona #56-C |
| CI | `providers.ci` | `CIProvider` | github (consumed by `nvoi ci init` only) |

**InfraProvider contract** (`pkg/provider/infra.go`): every backend yields a `*kube.Client` via `Bootstrap` (write — converges drift) or `Connect` (read-only — attach to existing infra). Reconcile branches on none of: provider name, IngressBinding type, NodeShell-or-not, ConsumesBlocks. Adding a new backend = implementing the interface; zero reconcile changes.

## Credential resolution

Every credential — provider tokens, SSH key, service `$VAR` — goes through `provider.CredentialSource`, built once at `cmd/cli/context.go::credentialSource(ctx, cfg)`.

| `providers.secrets` | Source | How it reads |
|---|---|---|
| unset | `EnvSource{}` | `os.Getenv(k)` — values from shell / `.env` |
| `doppler` \| `awssm` \| `infisical` | `SecretsSource{ctx, provider}` | backend's direct API at deploy time — no env fallback |

Scalar (`secrets: infisical`) matches the other provider keys; struct form (`secrets: {kind: ...}`) also accepted for future per-backend knobs.

**Strict mode when a backend is declared:** the backend IS the source. The only escape hatch is the backend's own bootstrap creds (e.g. `INFISICAL_CLIENT_ID`+`INFISICAL_CLIENT_SECRET`+`INFISICAL_PROJECT_SLUG`) read from env. Everything else — infra / DNS / storage creds, SSH private key, service `$VAR` — resolves through the backend. Misconfigured backend → hard error at startup via `ValidateCredentials`.

**Adapters are direct-API, never shell-outs** (Kamal shells out to vendor CLIs; nvoi calls the REST/SDK directly). See `pkg/provider/CLAUDE.md` for the registration pattern.

| Credential | Env mode | Backend mode |
|---|---|---|
| Infra / DNS / Storage | env (schema `EnvVar`) | backend.Get(`EnvVar`) |
| Service `$VAR` in `secrets:` | env | backend.Get(`VAR`) |
| SSH private key | `SSH_PRIVATE_KEY` → `SSH_KEY_PATH` → `~/.ssh/id_ed25519` → `~/.ssh/id_rsa` | `source.Get("SSH_PRIVATE_KEY")` → `SSH_KEY_PATH` → disk fallback |
| Backend's own creds | — | env (bootstrap) |

## Working tree

The working tree frequently has uncommitted changes — that's normal. The on-disk file is always the intended version. Never flag a mismatch between a prior commit and the working tree as a bug. Commits happen when the user asks.

## Key rules

1. **No state files.** Infrastructure is the truth. `deploy` is idempotent; run twice, same result.
2. **`app` + `env` in `nvoi.yaml` are the namespace.** Naming: `nvoi-{app}-{env}-{resource}`. Deterministic, no UUIDs.
3. **`os.Getenv` lives exclusively in `cmd/`.** `internal/`, `pkg/` never read env vars. `cmd/cli/context.go` builds the `CredentialSource`; everything downstream calls `source.Get(k)`.
4. **Providers are silent.** Never print or narrate. Output via `pkg/core/` → `Output` interface. `pkg/core/` itself never writes to stdout.
5. **Every provider operation goes through `pkg/core/`.** No caller invokes a provider method directly. `pkg/core/` wraps every op with output, error handling, naming.
6. **Every kube op goes through `*kube.Client`.** Exported methods on `Client` are the only way `internal/reconcile` or `pkg/core/` talk to the apiserver. No inline kubectl strings, no direct client-go usage outside `pkg/kube/`.
7. **`pkg/core/` never imports `net/http`.** HTTP belongs in `infra/` or `provider/`.
8. **Errors flow up, render once.** `pkg/core/` returns errors; Cobra renders via `Output.Error()`.
9. **Input validated once at the boundary.** `ValidateConfig` is the only place that validates user input. Internal code trusts validated input — no defensive escaping. Validators in `pkg/utils/naming.go`: `ValidateName` (DNS-1123), `ValidateEnvVarName` (POSIX), `ValidateDomain`.
10. **Async provider actions polled to completion.** Every action that returns an ID must be polled via `waitForAction` before proceeding. Fire-and-forget = production race condition.
11. **`DeleteServer` detaches firewall before termination.** Every provider. Hetzner: `detachFirewall` + poll. AWS: move to VPC default SG. Scaleway: reassign to project default SG. `DeleteFirewall` retries "still in use."
12. **Single binary, one mode.** `cmd/cli` reads `nvoi.yaml`, resolves credentials via `CredentialSource`, calls `internal/reconcile` / `pkg/core/` directly. No server, no relay, no custody. SSH keys injected via cloud-init only.

## Production hardening notes

- **`~` doesn't expand in Go.** `resolveSSHKey()` handles tilde expansion against `$HOME`.
- **`Client.Apply` is typed-only.** Every kind we ship is dispatched through the typed clientset (Get → Create-if-missing → Update-otherwise) with `FieldManager: "nvoi"`. There is no dynamic / SSA fallback — unknown kinds error. For Deployment/StatefulSet, `Apply` preserves `.status` from the existing object so re-running a Ready workload doesn't reset readiness in tests (mirrors apiserver status-subresource semantics).
- **Every typed Apply retries on conflict.** Controllers bump `ResourceVersion` asynchronously between our Get and Update. `pkg/kube/apply.go` wraps every Get/Update pair in `retry.RetryOnConflict(retry.DefaultRetry, …)`. Hit this live on a Caddy re-deploy — standard client-go hygiene, prevents every future variant.
- **Ingress is in-cluster Caddy (not Traefik).** k3s `--disable traefik --disable servicelb`. A `caddy:2.10-alpine` Deployment in `kube-system` with `nodeSelector: nvoi-role=master` and `hostPort: 80/443`. Config built from `cfg.Domains` + resolved Service ports, POSTed to Caddy's admin API on `localhost:2019` *inside the pod* via `kube.Client.Exec` — admin never exposed off-pod. Atomic listener swap, no connection drops. ACME state on a 1Gi PVC at `/data`. Details in `internal/reconcile/CLAUDE.md`.
- **HTTPS verification is two-step, both probes inside the Caddy pod.** `WaitForCaddyCert` polls for the cert on `/data`; `WaitForCaddyHTTPS` curls the service. No dependency on the operator's local DNS. Timeouts warn-and-continue.
- **Private registry credentials are k8s `imagePullSecrets`, not containerd config.** `registry:` block → `kubernetes.io/dockerconfigjson` Secret `registry-auth`; `BuildService`/`BuildCronJob` inject `imagePullSecrets` when `len(cfg.Registry) > 0`. No `registries.yaml` on the host, no Docker daemon.
- **Local builds are optional and opt-in per service.** `PreflightBuildx` fires once so missing buildx surfaces with an actionable hint. Kamal-style auth via the real `~/.docker/config.json`. Runs PRE-infra via `BuildRunner`.
- **Firewall orphan sweep deferred until after `ServersRemoveOrphans`.** `Firewall()` reconciles the desired per-role set early so new servers get rules before workloads land. `FirewallRemoveOrphans()` runs later because Hetzner rejects `DeleteFirewall` while a server is still attached — `DeleteServer`'s contract detaches first. `ensureFirewall` never resets rules.
- **Root disk size is creation-only.** `disk` applies at `EnsureServer`. Changing it on an existing server has no effect — resize requires recreation. Hetzner rejects `disk` at config time (fixed per server type).

## CI onboarding (`nvoi ci init`)

`providers.ci` is **opt-in, non-custody SaaS-mode onboarding**. Never consumed by `reconcile.Deploy` — the field is pure metadata read only by `nvoi ci init`. A config with `providers.ci: github` still deploys identically from the laptop; the only new capability is porting every credential into the CI provider's secret store and committing a deploy workflow so `git push` becomes the deploy trigger.

### Flow

`nvoi ci init` (in `cmd/cli/ci.go::runCIInit`):

1. `ValidateConfig` — same gate as `deploy`. `providers.ci` typo caught here.
2. `provider.ResolveCI(ciName, ciCreds)` + `ValidateCredentials(ctx)` — fail fast before any mutation. `ciCreds["repo"]` auto-inferred from `git remote get-url origin` when unset.
3. `collectCISecrets(cfg, source)` walks every source the runner will need:
   - Each declared provider's schema fields (infra/dns/storage/build/tunnel).
   - Secrets-backend bootstrap env (the one escape hatch that stays env-native — the backend can't authenticate to itself).
   - `SSH_PRIVATE_KEY` via the same `resolveSSHKey` the laptop uses — load-bearing. Hard error if empty; the runner can't dial the master without it.
   - `cfg.Secrets` + service/cron `secrets:` refs — bare `FOO` OR RHS `$VAR` of `ALIAS=$BAR`. The LHS (`ALIAS`) is the service-visible env name and must NOT be ported — the runner resolves `$BAR`.
   - Registry username / password `$VAR` refs.
4. `ciProv.SyncSecrets(ctx, secrets)` — uploads every collected secret (libsodium sealed-box via curve25519 on the GitHub path).
5. `ciProv.RenderWorkflow(...)` — deterministic `.github/workflows/nvoi.yml` with sorted `secretEnv` (byte-identical across re-runs when the set is unchanged → the Contents API diff stays quiet).
6. `ciProv.CommitFiles(ctx, files, msg)` — direct push to the default branch when it accepts direct pushes; otherwise feature branch (`nvoi/ci-init`) + PR. Protection detection: rulesets first, then classic protection, with 403-on-list treated as protected and 422 "repository rule violations" as the inline fallback trigger. Idempotent re-runs reuse the existing PR.

### Parser sharing

`cmd/cli/ci.go` and `internal/reconcile/envvars.go` both enumerate `$VAR` references — `cmd/` to know which env vars to port into the CI secret store, `reconcile` to resolve them at deploy time on the runner. Diverging rules (cmd thinks `${FOO_BAR}` is a var and reconcile doesn't, or vice versa) would be a silent source of wrong behavior between the laptop and the runner. Both paths share `pkg/utils/envvars.go` (`HasVarRef` / `ExtractVarRefs` / `IsVarStart` / `IsVarChar`) and `kube.ParseSecretRef` — one definition, one source of truth.

### Composition

Every `(build, ci)` pair composes freely — no coupling. `providers.build: ssh` + `providers.ci: github` is the remote-builder-on-CI path; `providers.build: local` + `providers.ci: github` keeps the build on the runner. Validator enforces the two independently.

## Tunnel ingress

`providers.tunnel` is an **optional, opt-in alternative** to the default Caddy + ACME + public-master-IP path. When set, the tunnel agent (cloudflared or ngrok) handles all inbound traffic — master ports 80/443 stay firewalled, TLS terminates at the provider edge.

### Composition matrix

| `infra` | `tunnel` | Flow |
|---|---|---|
| hetzner / aws / scaleway | _(unset)_ | Caddy + master public IP + DNS A record + ACME. Zero dependencies. |
| hetzner / aws / scaleway | `cloudflare` / `ngrok` | Tunnel agent in cluster, Caddy skipped, master `:80/:443` closed, DNS CNAME to tunnel edge, TLS at provider edge. |

### How it works

1. `reconcile.TunnelIngress` calls `TunnelProvider.Reconcile(ctx, TunnelRequest{Name, Routes})` — upserts the provider-side tunnel (idempotent, name-keyed as `nvoi-{app}-{env}`), pushes the full route table, fetches the agent token.
2. `TunnelPlan.Workloads` (Deployment + Secret ± ConfigMap) are applied to the cluster via `*kube.Client`.
3. `TunnelPlan.DNSBindings` (hostname → CNAME target) are written via the configured `DNSProvider` — **tunnel providers never write DNS directly**.
4. On the Caddy path when `providers.tunnel` was previously set, `PurgeTunnelAgents` removes orphaned agent workloads before Caddy starts.
5. On the tunnel path, `PurgeCaddy` removes orphaned Caddy workloads (no longer consuming hostPort 80/443).

### Teardown ordering

`nvoi teardown` deletes the provider-side tunnel **after** the agent Deployment is gone. Cloudflare rejects `DELETE /cfd_tunnel/{id}` while connections are active — the ordering guarantees success.

> **Known gap:** if `providers.tunnel` is removed from `nvoi.yaml` and a re-deploy is run (Caddy restored), the k8s-side orphan is cleaned up by `PurgeTunnelAgents`, but the **provider-side** tunnel (Cloudflare Tunnel object / ngrok reserved domains) is left until `nvoi teardown` or a manual cleanup. No state files means no record of the previous provider. Tracked for a future reconcile pass.

### Per-provider details

**Cloudflare Tunnel** (`pkg/provider/cloudflare/`)
- Credentials: `CLOUDFLARE_API_TOKEN`, `CLOUDFLARE_ACCOUNT_ID`
- Lookup always includes `is_deleted=false` — Cloudflare soft-deletes tunnels for ~30 days; omitting the filter causes name collisions with the tombstone.
- Ingress config pushed via `PUT /accounts/{acct}/cfd_tunnel/{id}/configurations`. Last rule is always `http_status:404` — unmatched hostnames get 404 at the edge, never reach the agent.
- Agent: `cloudflare/cloudflared:2024.8.3`, 2 replicas, `50m/64Mi` requests, `200m/128Mi` limits.

**ngrok** (`pkg/provider/ngrok/`)
- Credentials: `NGROK_API_KEY`, `NGROK_AUTHTOKEN`
- One reserved domain per hostname — looked up by name, created on first deploy, deleted on teardown.
- Agent: `ngrok/ngrok:3.20.0`, 2 replicas, authtoken from Secret, per-tunnel config in ConfigMap mounted at `/etc/ngrok.yml`.

### Testing fakes

- `internal/testutil/cloudflaretunnelfake.go` — `CloudflareTunnelFake` (httptest + OpLog). Enforces `is_deleted=false` on every lookup (returns 400 otherwise).
- `internal/testutil/ngrokfake.go` — `NgrokFake` (httptest + OpLog).

## Testing — mock governance

**One pattern. One file. No class-rewrite mocks.** Violations are reverted in review.

Only three boundaries are mocked:

| Boundary | Mock | Rationale |
|---|---|---|
| HTTP to cloud provider APIs (Hetzner / Cloudflare / AWS / Scaleway) | `internal/testutil/providermocks.go` — `HetznerFake`, `CloudflareFake` | Real wire protocol + real provider client — no in-process reimplementation of provider semantics. |
| SSH protocol to provisioned servers | `internal/testutil/mock_ssh.go` — `MockSSH` | Real SSH command/upload protocol, canned prefix responses. |
| Kubernetes apiserver | `internal/testutil/kubefake/kubefake.go` — `KubeFake` | Wraps client-go's own fake typed clientset. |

Nothing else. `MockOutput` is an internal UI-event capture, not a boundary mock.

**Hard rules:**

1. **One file for provider mocks.** `internal/testutil/providermocks.go`.
2. **No test declares a provider-interface type.** No `type myFooMock struct{...}` implementing `ComputeProvider` / `DNSProvider` / `BucketProvider`. Real provider clients are exercised end-to-end against httptest-backed fakes.
3. **Tests seed state, never stub behavior.** `fake.SeedServer(...)` / `SeedVolume` / `SeedFirewall` / `SeedNetwork` / `SeedDNSRecord` / `SeedBucket`. No `func(req) error` hooks.
4. **The `OpLog` is the assertion surface.** `fake.Has("ensure-server:X")` / `Count("delete-server:")` / `IndexOf` / `All`. New ops = one handler edit in `providermocks.go`.
5. **Error injection is explicit.** `fake.ErrorOn("delete-firewall:...", err)`. The matching HTTP handler short-circuits to 500. No `if testMode then ...` in production code.
6. **Registration is per-test.** `fake := testutil.NewHetznerFake(t); fake.Register("test-name")`. Re-registration replaces. A fresh `convergeDC` re-registers the shared defaults under `test-reconcile` for each test.
7. **`testutil.MockCompute` / `MockDNS` / `MockBucket` are deleted.** Any PR reintroducing a provider-interface-satisfying mock type in any file fails review — extend `providermocks.go` instead.
8. **No shell injection.** Secrets flow through the typed kube client — no shell interpolation of user data.

**Writing a new test:**

- Compute? `fake := testutil.NewHetznerFake(t); fake.Register("provider-name")`. Seed via `fake.SeedServer(...)` etc.
- DNS or R2? `cf := testutil.NewCloudflareFake(t, opts); cf.RegisterDNS(...)` / `cf.RegisterBucket(...)`.
- SSH? `testutil.MockSSH` with `Prefixes`.
- Kube? `kubefake.NewKubeFake()`; set `Cluster.MasterKube = kf.Client`.
- Reconcile end-to-end? `convergeDC(log, convergeMock())` — wires MockSSH + KubeFake + provider fakes; assert via `kfFor(dc)`.

Read the file-top comment in `providermocks.go` before extending it.
