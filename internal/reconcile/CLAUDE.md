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
  → sync Cluster.Provider from cfg.Providers.Infra (legacy callers)
  → dc.Cluster.DeployHash = now.UTC().Format("20060102-150405")
  → BuildImages(ctx, dc, cfg, platform)     — LOCAL docker login/build/push for services.X.build; PRE-infra
  → infra = provider.ResolveInfra(...)      — single provider instance for the whole deploy; defer Close()
  → kc = infra.Bootstrap(ctx, bctx)         — provisions servers + firewall + volumes; returns *kube.Client
  → ns = infra.NodeShell(ctx, bctx)          — optional SSH for `nvoi ssh`; nil for sandbox/managed
  → kc.EnsureNamespace(app-ns)
  → Registries(ctx, dc, cfg)                — resolve `registry:` creds → dockerconfigjson Secret
  → secretValues = Secrets(ctx, dc, cfg)
  → storageCreds = Storage(ctx, dc, cfg)
  → sources = mergeSources(secretValues, storageCreds)
  → Services(ctx, dc, cfg, sources)         — kc.SweepOwned(owner=services) for orphan sweep, KnownVolumes from cfg
  → Crons(ctx, dc, cfg, sources)            — kc.SweepOwned(owner=crons) for orphan sweep
  → infra.TeardownOrphans(ctx, bctx)        — drain orphan servers + sweep orphan firewalls + orphan volume delete
  → IF infra.HasPublicIngress() && len(cfg.Domains) > 0:
       RouteDomains(ctx, dc, cfg, infra, bctx) — dns.RouteTo(domain, infra.IngressBinding(svc))
       verifyDNSPropagation(ctx, dc, cfg)        — warn-only, before ACME
       Ingress(ctx, dc, cfg)                     — Caddy admin-API hot-reload + per-domain cert/HTTPS waits
```

**Zero per-provider branching.** Adding a new infra backend = implementing
`pkg/provider/infra.go::InfraProvider`. Reconcile branches on three gates
only: `HasPublicIngress()`, `NodeShell != nil`, `ConsumesBlocks()`.

## Convergence pattern

Each resource type follows:

```
for each desired resource:
    kc.ApplyOwned(ctx, ns, owner, obj)  ← idempotent (create or update); stamps
                                          app.kubernetes.io/managed-by=nvoi +
                                          nvoi/owner=<owner>

kc.SweepOwned(ctx, ns, owner, kind, desired)  ← lists nvoi/owner=<owner>
                                                  resources of `kind` in ns,
                                                  deletes anything not in
                                                  `desired`
```

Some steps split the two halves across the flow (Servers, Firewall) because deletion is blocked until later invariants hold.

**Label discipline.** Every k8s object reconcile creates goes through
`kc.ApplyOwned`, which stamps `nvoi/owner=<step>` from a closed
taxonomy: `services` / `crons` / `databases` / `database-branches` /
`tunnel` / `caddy` / `registries`. The owner label is the single
discriminator for orphan sweep — `kc.SweepOwned` scopes its listing by
this label, so each step's sweep can never see another step's
resources. No exclusions, no special-casing, no `LabelNvoiDatabase`-
style band-aids. Constants live in `pkg/utils/naming.go`.

## Step notes

### BuildImages (pre-infra)

`images.go`. Runs before any infra. A build failure aborts the deploy before a server is provisioned.

Named `BuildImages` (not `Build`) to free the word "build" for the outer `BuildProvider` family in `pkg/provider/build.go` — the outer "build" is the substrate a deploy runs on (`local` / `ssh` / `daytona`); this inner step is specifically the per-service dispatch loop invoked by `reconcile.Deploy`. `BuildImages` resolves the selected `BuildProvider` once via `provider.ResolveBuild`, then walks `cfg.Services` in sorted order and calls `bp.Build(ctx, req)` per service with a `build:` directive. The returned image ref is what `Services()` stamps on the PodSpec.

Provider-specific mechanics live entirely inside each `BuildProvider`:

- `local` — shells out to `docker login` + `docker buildx build --push` on the operator's machine via `pkg/core/build.go::BuildService` (the `BuildRunner` interface — `DockerRunner` in prod, fakes in tests).
- `ssh` — dials a `role: builder` server via SSH, clones `req.GitRemote @ req.GitRef`, runs `docker buildx build --push` there.
- `daytona` — boots a Daytona sandbox, clones the same git ref, runs the buildx-and-push loop inside the sandbox over Daytona's session-exec API.

`reconcile.Deploy` provisions builders (`infra.ProvisionBuilders` + `infra.BuilderTargets`) before `BuildImages` when the resolved provider declares `BuildCapability.RequiresBuilders = true`. The resulting `[]BuilderTarget` is threaded into every `BuildRequest` — `ssh` consumes it, `local`/`daytona` ignore it. Git source (`req.GitRemote`, `req.GitRef`) is inferred by the CLI from the operator's cwd (`git remote get-url origin` + `git rev-parse HEAD`); remote providers (`ssh`, `daytona`) hard-error when those strings are empty.

Image tag resolution (Kamal-style, adapted): host inferred when `image:` has no host and exactly one `registry:` entry is declared; ambiguous with multiple registries; bare shortnames rejected under `build:`. User tag (if any) preserved AND suffixed with `dc.Cluster.DeployHash` → guarantees a new `image:` string every deploy so the rollout controller always restarts pods. Digest-pinned references pass through unmodified. Logic in `image.go`.

### Infra (Bootstrap / LiveSnapshot / TeardownOrphans / Teardown)

Owned entirely by the InfraProvider — `internal/reconcile/{servers,firewall,volumes}.go` were deleted in #47-C6. The orchestration lives in `pkg/provider/infra/{hetzner,aws,scaleway}/infra.go`.

- `infra.Bootstrap`: provisions servers (masters then workers, swap + k3s install/join + node label), firewalls (per-role set), volumes (create + SSH-mount). Returns the `*kube.Client` tunneled through the master SSH it dialed. Caches the SSH on the receiver so `infra.NodeShell` returns the same connection.
- `infra.LiveSnapshot`: reads provider-side state (servers, volumes, firewalls) for orphan detection. Called internally by `infra.TeardownOrphans`; not invoked from reconcile.
- `infra.TeardownOrphans`: drains orphan servers (via `Cluster.MasterKube.DrainAndRemoveNode`), sweeps orphan firewalls AFTER server detachment (Hetzner rejects `DeleteFirewall` on attached resources — `DeleteServer`'s contract detaches first), best-effort orphan volume delete (warn-on-fail).
- `infra.Teardown`: hard nuke for `bin/destroy`. Workers → master → firewalls → network. With `--delete-volumes`, volumes nuked first; otherwise detached on server delete and preserved.

A volume is pinned to a physical server via `server:` in the config. Cross-server mount = hard validate error. Volume-mounting service → StatefulSet (not Deployment).

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

Orphan services / crons → `kc.SweepOwned(owner=services|crons, ...)` per kind (Deployment + StatefulSet + Service + Secret for services; CronJob + Secret for crons). Orphan key cleanup inside `{name}-secrets` is per-key (a service that drops keys from its `secrets:` block keeps the Secret but removes those keys).

### DNS (RouteDomains)

`reconcile.go::RouteDomains`. Pure provider API — no SSH. For each `(service, domain)` pair, calls `infra.IngressBinding(svc)` to learn how the provider exposes the service (IaaS: `{DNSType:"A", DNSTarget: master.IPv4}`; managed-k8s would return CNAME), then `dns.RouteTo(domain, binding)` writes the appropriate record. Orphan detection is per-service: a service that had domains in `live.Domains` but is gone from config → `dns.Unroute(domain)`. CNAME bindings rejected in v1 (tracked in #48 / #49).

### verifyDNSPropagation

`dns_verify.go`. Warn-only preflight before ACME. Reads expected target from `infra.IngressBinding` (skips the check entirely for non-A bindings — propagation only meaningful for IPv4/IPv6 targets). Resolves each configured domain against the master node's resolver via SSH; if the answer doesn't match, emits a warning so the operator knows the next step may legitimately wait on propagation. Never fails the deploy.

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

Reconcile tests use `convergeDC(log, convergeMock())` — see `helpers_test.go`. Wires a `MockSSH` + `KubeFake`, registers HetznerFake httptest-backed provider fake, and registers each fake in `kubeFakes` keyed by `dc` so tests assert via `kfFor(dc)`. The pre-injected `Cluster.MasterKube = kf.Client` is propagated through `BootstrapContext.MasterKube` so `infra.Bootstrap` returns the KubeFake instead of dialing a real tunnel — tests exercise the full Deploy() path end-to-end without an SSH-tunneled apiserver.

`reconcile_test.go` (the invariant suite) covers the load-bearing properties of #47:

- **First deploy**: `ensure-server` op fires, namespace ensured.
- **Idempotency**: Deploy + Deploy = no duplicate `ensure-server` ops.
- **Orphan handling**: worker dropped from cfg gets drained + deleted AFTER master ensure.
- **Validation gate**: missing `providers.infra` errors before any provider op.
- **No `providers.compute` alias**: legacy YAML rejected at validation time.

Each test asserts an OUTCOME (final fake state, ordering position) — not a command sequence — so assertions stay valid as orchestration evolves.

Mock governance rules are in the repo-root `CLAUDE.md` — provider-mock types live in `internal/testutil/providermocks.go`, no per-test provider-interface mocks, ops asserted via `fake.Has(...)` / `Count` / `IndexOf`. Every test in this package obeys those rules.
