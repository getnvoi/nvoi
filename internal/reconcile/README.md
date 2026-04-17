# internal/reconcile

Deploy-time convergence engine. Takes a YAML config (desired state) and live cluster state (current state), then converges: adds what's missing, removes what's orphaned. Every run leaves the cluster matching the config, regardless of what state it was in before.

## Two operations

- **Reconcile** (`Deploy`) — diff-based. Queries live state, walks each resource type in order, converges. Day-to-day operator.
- **Teardown** (`internal/core/teardown.go`) — hard nuke. No diff, no live state query. Wipes external provider resources. Kill switch.

Reconcile manages everything: provider infra AND k8s resources. Teardown only touches external provider resources — k8s dies with the servers.

## Reconciliation order

Strict sequence. Each step depends on the previous:

1. **Servers** — masters first, then workers. Orphans drained and removed.
2. **Firewall** — apply port rules. No orphan concept (single resource, replaced in full).
3. **Volumes** — create, attach, mount. Orphans unmounted and deleted.
4. **Build** — build container images. No orphan concept (registry is append-only).
5. **Secrets** — set from environment. Orphan keys removed from global k8s secret.
6. **Packages** — database StatefulSets, headless Services, backup buckets, backup CronJobs. Returns env vars for $VAR resolution.
7. **Storage** — create buckets, return S3 credentials. Orphan buckets emptied and deleted.
8. **Services** — deploy workloads (Deployment or StatefulSet). Per-service k8s Secrets hold resolved secrets + storage creds. Orphans deleted via kubectl.
9. **Crons** — deploy CronJobs. Per-cron k8s Secrets hold resolved secrets + storage creds. Orphans deleted via kubectl.
9. **DNS** — create A records via DNS provider. Orphan records deleted.
10. **Ingress** — deploy Caddy routes. Orphan routes removed from Caddyfile.

## Convergence rules

Each resource type follows the same pattern:

```
for each desired resource:
    set(resource)  ← idempotent, create or update

if live state exists:
    for each live resource NOT in desired:
        delete(resource)  ← orphan removal
```

### Servers

- Masters created before workers (workers need the master to join the cluster).
- Orphan servers are drained (`kubectl drain` + `kubectl delete node`) before provider deletion.
- `ComputeSet` is a full orchestrator: create server → wait SSH → install Docker → install k3s (master) or join cluster (worker) → label node.
- Firewall and network are never touched by reconcile — those are shared resources managed by teardown only.
- `DeleteServer` only deletes the server. Detaches volumes first. Never deletes firewall or network.

### Volumes

- A volume is pinned to a physical server via `server:` in the config.
- A service/cron mounting a volume is auto-pinned to that volume's server via `ResolveServer()`.
- **A workload cannot be on a different server than its volume.** `ValidateConfig` enforces this: if `service.server` is set and differs from `volume.server`, it's a hard error ("cannot move").
- Volume → StatefulSet (not Deployment). Replicas forced to 1.
- `VolumeSet` creates the volume at the provider, then SSH-mounts it (mkfs.xfs if unformatted, fstab entry, mountpoint verification).
- `VolumeDelete` unmounts via SSH on every server, then deletes at the provider.
- Orphan volumes are unmounted and deleted.

### Firewall

- Single resource per cluster (named `nvoi-{app}-{env}-fw`).
- `firewall: default` opens ports 80 and 443 to `0.0.0.0/0`. Custom rules via `port:cidr` format.
- Internal ports (6443, 10250, 8472, 5000) always preserved — never user-configurable.
- SSH (22) defaults to open, overridable.
- Empty firewall config = skip (no-op).
- Reconcile applies rules. Teardown deletes the firewall resource.

### Build

- No live state query. No orphan concept. Registry is append-only.
- Single target → `BuildRun`. Multiple targets → `BuildParallel`.
- Build is skipped entirely if `build:` is empty in config.
- Image refs are resolved at service/cron deploy time via `resolveImageRef()` → `BuildLatest`.

### Secrets

- Values resolved from viper (environment variables) at deploy time.
- Bare names (no `=`) resolved from env. `$VAR` references on the right side of `=` also resolved from env — no bare declaration required first.
- Missing secret in environment = hard error (fail-fast, not silent skip).
- Global secrets (`cfg.Secrets`) stored in a single k8s Secret named `secrets` in the namespace.
- Per-service/cron secrets stored in `{name}-secrets` k8s Secrets (see Services/Crons below).
- Orphan keys (in live but not in config) are removed from the global k8s secret.
- `SecretSet` patches the k8s secret via JSON merge patch (uploaded as file, not inline — no shell injection).

### Storage

- `StorageSet` creates the bucket at the provider and returns 4 S3 credentials (endpoint, bucket, access key, secret key). It does NOT write to k8s — credentials flow into per-service/cron secrets via `mergeSources`.
- CORS and lifecycle rules applied if configured.
- Orphan buckets are emptied then deleted. Package-managed buckets (database backups) are protected.
- `storage:` on a service/cron expands credentials into the per-service k8s Secret.
- `cfg.StorageNames()` is the single source of truth for what storage exists (user-declared + database backup buckets).

### Services

- `image` or `build` — mutually exclusive. `build` resolved to latest image ref via `BuildLatest`.
- `server:` pins to a node via `nodeSelector` on the k8s label `nvoi.io/role={server}`.
- Volume mounts auto-pin the service to the volume's server (via `ResolveServer`).
- Volume in mounts → StatefulSet. No volumes → Deployment.
- **Per-service secrets:** each service gets a `{name}-secrets` k8s Secret holding resolved `secrets:` entries + expanded `storage:` credentials. Secrets with `$VAR` references resolve from unified sources (env vars, package env vars, storage creds).
- **Orphan key cleanup:** keys in the per-service secret that are no longer declared are removed.
- **Package-managed services** (database StatefulSets) are protected from orphan deletion via `db.ServiceName`.
- WaitRollout runs on the **last** service only (all previous are deployed without waiting). Terminal states (`CrashLoopBackOff`, `ImagePullBackOff`, `OOMKilled`) exit immediately with logs.
- `ServiceDelete` removes the Deployment/StatefulSet AND the k8s Service by name.

### Crons

- Same conventions as services: `image` or `build`, `server:` pinning, secret/storage/volume references.
- Each cron gets a `{name}-secrets` k8s Secret (same pattern as services).
- `schedule` is required (cron expression).
- **Package-managed crons** (database backup CronJobs) are protected from orphan deletion via `db.BackupCronName`.
- Orphan CronJobs deleted via kubectl.

### DNS

- **No SSH.** DNS operations are pure provider API calls (Cloudflare, AWS Route53, Scaleway).
- `DNSSet` creates A records pointing to the master's IPv4. Needs a running master (calls `Cluster.Master()`).
- Orphan detection is per-service: if a service had domains in live but is gone from config, its DNS records are deleted.
- DNS and ingress are separate concerns — DNS creates records, ingress creates Caddy routes.

### Ingress

- Deploys Caddy as a Deployment with `hostNetwork: true`, `Recreate` strategy.
- Takes all `service:domain` mappings, builds a Caddyfile, deploys as ConfigMap.
- Caddy hot-reloads after ConfigMap update (polls for config sync, then `caddy reload`).
- Service must have a port > 0 (resolved via `kubectl get service`).
- TLS is ACME only (Let's Encrypt).
- Orphan routes removed from Caddyfile. If no routes remain, Caddy deployment is deleted.

## Edge cases

- **First deploy (live is nil):** No orphan detection. Everything created from scratch.
- **Already converged:** Set calls are idempotent. No orphan deletions. Same result.
- **Partial failure:** Reconcile stops on first error. Re-run converges from wherever it left off.
- **Manual drift:** Someone manually added a server, deleted a volume, created a rogue service. Reconcile detects the diff and fixes it.
- **Scale up:** Add workers to config. Reconcile creates them. No deletions.
- **Scale down:** Remove workers from config. Reconcile drains and deletes them.
- **Complete replacement:** Config has entirely different services than live. Old ones deleted, new ones created.
- **Volume server mismatch:** Config says service on `worker-1` but volume on `master`. Hard error before touching infra.

## SSH command chains

Every `pkg/core/` function that touches the cluster does so via SSH. The full command chains for each function:

### ComputeSet (master path)
1. `command -v kubectl ... && sudo k3s kubectl get nodes ... | grep -q ' Ready '` — check k3s installed
2. `ip -o -4 addr show | awk '/{privateIP}/{print $2}' | head -1` — discover private interface
3. `sudo mkdir -p /etc/rancher/k3s` + `sudo tee /etc/rancher/k3s/registries.yaml` — configure registry mirrors (containerd hint that the registry is plaintext HTTP)
4. `curl -sfL https://get.k3s.io | INSTALL_K3S_EXEC='server ...' sh -` — install k3s (RunStream)
5. `mkdir -p /home/deploy/.kube && sudo cp ... && sudo sed ...` — setup kubeconfig
6. `KUBECONFIG=... kubectl get nodes` — verify k3s ready (poll)
7. (k8s) Label node via apiserver — `kc.LabelNode`

The in-cluster Docker registry is no longer started here; it's a regular
k8s Deployment in `kube-system` applied later by the reconcile step
(`kc.EnsureRegistry` — see `pkg/kube/registry.go`). k3s ships its own
containerd; the host has no Docker daemon.

### ComputeSet (worker path)
1. `sudo cat /var/lib/rancher/k3s/server/node-token` — read join token (on master SSH)
2. `systemctl is-active --quiet k3s-agent` — check if already joined
3. Registry config + k3s restart (on worker SSH)
4. `ip -o -4 addr show ...` — discover private interface
5. `curl -sfL https://get.k3s.io | K3S_URL=... K3S_TOKEN=... sh -` — install k3s agent (RunStream)
6. `KUBECONFIG=... kubectl get nodes -o wide` — verify worker ready (on master SSH, poll)
7. (k8s) Label node via apiserver — `kc.LabelNode`

### VolumeSet
1. `mountpoint -q {path} && echo mounted || echo not` — check mounted
2. `test -b {device} && echo ready || true` — wait for device (poll)
3. `sudo mkdir -p {path}` — create mount dir
4. `sudo blkid {device} || true` — check if formatted
5. `sudo mkfs.xfs {device}` — format (if unformatted)
6. `sudo mount {device} {path}` — mount
7. `grep '{path}' /etc/fstab || true` — check fstab
8. `UUID=... | sudo tee -a /etc/fstab` — add fstab entry (if missing)
9. `sudo xfs_growfs {path}` — grow filesystem
10. `mountpoint -q {path} ...` — verify mounted

### VolumeDelete
Per server: `mountpoint` check → `sudo umount -f` → `sudo sed -i ... /etc/fstab` → `sudo rmdir`

### ServiceSet / CronSet
1. `kubectl create namespace ... --dry-run=client -o yaml | kubectl apply -f -`
2. Upload YAML → `kubectl replace -f` or `kubectl apply --server-side -f`

### ServiceDelete
`kubectl delete deployment/{name}` + `kubectl delete statefulset/{name}` + `kubectl delete service/{name}`

### CronDelete
`kubectl delete cronjob/{name}`

### SecretSet
1. `kubectl get secret {name}` — check exists
2. `kubectl create secret generic ...` (new) or upload patch + `kubectl patch secret ...` (update)

### SecretDelete
`kubectl get secret` → `kubectl patch secret --type=json -p '[{"op":"remove",...}]'`

### IngressSet
1. `kubectl get service {name} -o jsonpath='{.spec.ports[0].port}'` — resolve port
2. `kubectl get configmap {name} -o jsonpath='{.data.Caddyfile}'` — read current routes
3. Upload ConfigMap + `kubectl apply`
4. Check/create Caddy Deployment
5. `kubectl get pods -l ... -o jsonpath='{.items[0].metadata.name}'` — find Caddy pod
6. `kubectl exec {pod} -- caddy reload --config /etc/caddy/Caddyfile --force`

### IngressDelete
Read current routes → remove route → update ConfigMap (or delete Caddy if no routes remain)

### No SSH
`DNSSet`, `DNSDelete`, `FirewallSet`, `StorageEmpty`, `ComputeDelete` — provider API only.
