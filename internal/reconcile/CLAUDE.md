# CLAUDE.md — internal/reconcile

Deploy-time convergence engine. Takes `nvoi.yaml` (desired state) + live cluster state (current) and converges: adds what's missing, removes what's orphaned. Every run leaves the cluster matching the config regardless of prior state.

## Two operations

- **Reconcile** (`Deploy()` in `reconcile.go`) — diff-based. Walks each resource type in order, idempotent. Day-to-day operator.
- **Teardown** (`internal/core/teardown.go`, invoked from CLI) — hard nuke. No diff, no live query. Wipes external provider resources. Volumes and storage preserved unless `--delete-volumes` / `--delete-storage`. k8s dies with the servers.

Reconcile manages provider infra AND k8s resources. Teardown only touches external provider resources.

## Reconcile flow

```
Deploy(ctx, dc, cfg)
  → ValidateConfig(cfg)
  → cfg.Resolve()                           — populate VolumeDef.MountPath, firewall names
  → dc.Cluster.DeployHash = now.UTC().Format("20060102-150405")
  → Build(ctx, dc, cfg)                     — LOCAL docker login/build/push for services.X.build; PRE-infra
  → live = DescribeLive(ctx, dc, cfg)
  → ServersAdd(ctx, dc, live, cfg)          — create desired; orphans NOT removed yet
  → establish MasterSSH + MasterKube
  → kc.EnsureNamespace(app-ns)
  → Registries(ctx, dc, live, cfg)          — resolve `registry:` creds → dockerconfigjson Secret (or orphan-delete)
  → Firewall(ctx, dc, live, cfg)            — desired per-role set; orphans NOT removed yet
  → Volumes(ctx, dc, live, cfg)
  → secretValues = Secrets(ctx, dc, live, cfg)
  → storageCreds = Storage(ctx, dc, live, cfg)
  → sources = mergeSources(secretValues, storageCreds)
  → Services(ctx, dc, live, cfg, sources)
  → Crons(ctx, dc, live, cfg, sources)
  → ServersRemoveOrphans(ctx, dc, live, cfg) — drain (Eviction API) + delete AFTER workloads moved
  → FirewallRemoveOrphans(ctx, dc, live, cfg) — sweep AFTER servers detached firewalls
  → DNS(ctx, dc, live, cfg)
  → verifyDNSPropagation(ctx, dc, cfg)       — warn-only, before ACME
  → Ingress(ctx, dc, live, cfg)              — Caddy admin-API hot-reload + per-domain cert/HTTPS waits
```

## Convergence pattern

Each resource type follows:

```
for each desired resource:
    set(resource)                     ← idempotent (create or update)

if live state exists:
    for each live resource NOT in desired:
        delete(resource)              ← orphan removal
```

Some steps split the two halves across the flow (Servers, Firewall) because deletion is blocked until later invariants hold.

## Step notes

### Build (pre-infra)

`build.go`. Runs before any infra. A build failure aborts the deploy before a server is provisioned. Runs locally — shells out to `docker buildx` on the operator's PATH via `pkg/core/build.go::BuildService` (through the `BuildRunner` interface — `DockerRunner` in prod, fakes in tests).

Single-service builds serialize; multi-service builds run via `BuildParallel`. One `PreflightBuildx` at the top of the pass so missing buildx surfaces once with an install hint instead of opaque per-service flag errors. Passwords flow via `--password-stdin`, never argv. Auth writes to the operator's real `~/.docker/config.json` (Kamal-style; DOCKER_CONFIG isolation breaks plugin discovery and context lookup — don't).

Image tag resolution (Kamal-style, adapted): host inferred when `image:` has no host and exactly one `registry:` entry is declared; ambiguous with multiple registries; bare shortnames rejected under `build:`. User tag (if any) preserved AND suffixed with `dc.Cluster.DeployHash` → guarantees a new `image:` string every deploy so the rollout controller always restarts pods. Digest-pinned references pass through unmodified. Logic in `image.go`.

### Servers split (Add / RemoveOrphans)

`servers.go`. Zero-downtime server replacement requires:

1. `ServersAdd` early — new servers exist and are k3s-joined before Services/Crons move workloads.
2. `ServersRemoveOrphans` late — drains old servers via `kc.DrainAndRemoveNode` (Eviction API, force-remove on NotReady) ONLY after Services/Crons have re-landed workloads elsewhere.

`ComputeSet` is the per-server orchestrator: `EnsureServer` → resolve private IP → clear stale known host → poll SSH until `Connect` succeeds → `EnsureSwap` (proportional to actual disk, 5% clamped 512 MiB – 2 GiB) → `InstallK3sMaster` or `JoinK3sWorker` → `LabelNode` via kube client. k3s ships its own containerd; no Docker daemon is installed on hosts.

### Firewall split (desired set / RemoveOrphans)

`firewall.go`. Firewall reconcile is split the same way for a different reason: a cloud provider's `DeleteFirewall` fails while the firewall is still attached to a server (Hetzner returns `resource_in_use`). `DeleteServer`'s contract detaches each firewall before termination, so the orphan sweep only succeeds AFTER `ServersRemoveOrphans` has run. Running the sweep inline inside `Firewall()` used to silently leave orphan firewalls behind.

`Firewall()` itself reconciles the desired per-role set (master-fw, worker-fw) and never resets rules on a pre-existing firewall (`ensureFirewall` is existence-only; rules are managed by `ReconcileFirewallRules`).

### Volumes

`volumes.go`. A volume is pinned to a physical server via `server:` in the config. Any workload mounting it is auto-pinned to the same server; cross-server mount = hard validate error. Volume-mounting service → StatefulSet (not Deployment). `VolumeSet` creates at the provider, then SSH-mounts (mkfs.xfs if unformatted, fstab entry, mountpoint verify). Orphans unmounted and deleted.

### Registries

`registries.go`. Top-level `registry:` block → `kubernetes.io/dockerconfigjson` Secret named `registry-auth` in the app namespace. `$VAR` values resolve through `dc.Creds` (same path as `secrets:`). `BuildService` / `BuildCronJob` inject `imagePullSecrets: [{name: registry-auth}]` into every PodSpec when `len(cfg.Registry) > 0`. Removing the block deletes the orphan Secret on the next deploy. This runs BEFORE Services/Crons because kubelet reads imagePullSecrets at first image pull.

### Secrets

`secrets.go`. Pure read — NO k8s writes. Collects every key referenced by `cfg.Secrets`, `cfg.Services[*].Secrets`, `cfg.Crons[*].Secrets` (bare names AND `$VAR` references inside `KEY=VALUE` strings), resolves each via `dc.Creds.Get(k)`, returns the resolved map. Values reach the cluster only inside per-service/cron k8s Secrets created by Services/Crons. `cfg.Secrets` entries that resolve to empty → hard error (no silent skip).

### Storage

`storage.go`. `StorageSet` creates each bucket at the provider (via `pkg/core/storage.StorageSet`). Returns a nested credentials map (`storageName → {endpoint, bucket, access_key, secret_key}`) that `mergeSources` expands into per-service secrets (only for services / crons that reference the storage). Orphans emptied then deleted. Does NOT write to k8s directly.

### Services / Crons

`services.go`, `crons.go`. Each workload gets a `{name}-secrets` k8s Secret holding resolved `secrets:` entries + expanded `storage:` credentials. `image:` or `build:` — mutually exclusive (validated). `build:` resolves to a fully-qualified image via `image.go::ResolveImage` using the DeployHash.

Every Deployment / StatefulSet / CronJob gets `nvoi/deploy-hash: <hash>` stamped on workload metadata AND pod-template metadata (but NOT selectors — selector changes orphan pods). Readable via `kubectl get deploy -L nvoi/deploy-hash`.

`server:` → `nodeSelector` on `nvoi.io/role=<server>`. `servers:` → `nodeAffinity` + `topologySpreadConstraints`. Web-facing services (with domains) default to `replicas: 2`; explicit `replicas: 1` is a hard validate error.

`WaitRollout` runs on the last service only — earlier services fire-and-forget so the rollouts pipeline. Terminal states (`CrashLoopBackOff`, `ImagePullBackOff`, `OOMKilled`) abort immediately with recent logs + events.

Orphan services / crons → `ServiceDelete` / `CronDelete`. Orphan key cleanup inside `{name}-secrets` is per-key.

### DNS

`dns.go`. Pure provider API — no SSH. Creates A records pointing to the master's IPv4 (needs `Cluster.Master()` resolved). Orphan detection is per-service: a service that had domains in `live` but is gone from config → its records deleted.

### verifyDNSPropagation

`dns_verify.go`. Warn-only preflight before ACME. Resolves each configured domain against public DNS; if the answer doesn't match the master IP yet, emits a warning so the operator knows the next step may legitimately wait on propagation. Never fails the deploy.

### Ingress (Caddy, not Traefik)

`ingress.go`. k3s is installed with `--disable traefik --disable servicelb`. A single `caddy:2.10-alpine` Deployment runs in `kube-system` with `nodeSelector: nvoi-role=master` and `hostPort: 80/443`; ACME state on a 1Gi PVC at `/data` (k3s `local-path`).

Flow:

1. `kc.EnsureCaddy(ctx)` — PVC + ConfigMap (seed config that binds `:80`) + Service + Deployment. Idempotent; reapplied every deploy → zero drift.
2. Build `CaddyConfig` JSON from `cfg.Domains` + resolved Service ports (`pkg/kube/caddy_config.go`, deterministic: sorted by service).
3. `kc.ReloadCaddyConfig(ctx, configJSON)` — POSTs to Caddy's admin API on `localhost:2019/load` *inside the pod* via `kube.Client.Exec`. Admin is never exposed off-pod. Caddy validates first; on success, listeners atomically swap with no connection drops. Removed domains drop out of the next config — no k8s `Ingress` objects exist, so there's nothing to orphan-delete.
4. Per-domain: `WaitForCaddyCert` polls for `/data/caddy/certificates/acme-v02.api.letsencrypt.org-directory/<domain>/<domain>.crt`, then `WaitForCaddyHTTPS` curls `https://<domain><health>` from inside the pod. Timeouts warn-and-continue (Caddy keeps retrying ACME; next deploy re-verifies).

## Edge cases

- **First deploy (live is nil):** No orphan detection. Everything created from scratch.
- **Already converged:** Set calls are idempotent. No orphan deletions. Same result.
- **Partial failure:** Reconcile stops on first error. Re-running converges from wherever it stopped.
- **Manual drift:** Rogue server / deleted volume / orphan service. Reconcile detects and fixes.
- **Scale up:** Add workers to config. Reconcile creates them. No deletions.
- **Scale down:** Remove workers. Reconcile drains (Eviction API) and deletes them.
- **Complete replacement:** Config has entirely different services. Old ones deleted, new ones created.
- **Volume-server mismatch:** Config says service on `worker-1` but volume on `master`. Hard error at `ValidateConfig` before touching infra.

## Testing

Reconcile tests use `convergeDC(log, convergeMock())` — see `helpers_test.go`. Wires a `MockSSH` + `KubeFake`, registers HetznerFake/CloudflareFake httptest-backed provider fakes, and registers each fake in `kubeFakes` keyed by `dc` so tests assert via `kfFor(dc)`.

Mock governance rules are in the repo-root `CLAUDE.md` — provider-mock types live in `internal/testutil/providermocks.go`, no per-test provider-interface mocks, ops asserted via `fake.Has(...)` / `Count` / `IndexOf`. Every test in this package obeys those rules.
