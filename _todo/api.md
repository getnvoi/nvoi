# API changes for shared command tree

The API already has endpoints for operational commands (database list, backup, agent exec/logs). This file covers what's missing for the config-mutation path and schema gaps.

---

## Config schema additions

### `internal/api/config/schema.go`

Add to `Service`:

```go
type Service struct {
    // existing...
    Image         string `json:"image,omitempty" yaml:"image,omitempty"`           // custom base image for managed (e.g. postgres:16)
    BackupStorage string `json:"backup_storage,omitempty" yaml:"backup_storage,omitempty"` // pre-existing storage name
    BackupCron    string `json:"backup_cron,omitempty" yaml:"backup_cron,omitempty"`       // schedule
}
```

These flow through `ResolveDeploymentSteps` → `managed.Compile()` via `managed.Request{Image, BackupStorage, BackupCron}`.

Add `Crons` to `Config`:

```go
type Cron struct {
    Image    string   `json:"image" yaml:"image"`
    Schedule string   `json:"schedule" yaml:"schedule"`
    Command  string   `json:"command,omitempty" yaml:"command,omitempty"`
    Server   string   `json:"server,omitempty" yaml:"server,omitempty"`
    Env      []string `json:"env,omitempty" yaml:"env,omitempty"`
    Secrets  []string `json:"secrets,omitempty" yaml:"secrets,omitempty"`
    Storage  []string `json:"storage,omitempty" yaml:"storage,omitempty"`
}

type Config struct {
    // existing...
    Crons map[string]Cron `json:"crons,omitempty" yaml:"crons,omitempty"`
}
```

### Ingress cleanup

Replace:

```go
type IngressConfig struct {
    Exposure string
    TLS      *IngressTLSConfig
    Edge     *IngressEdgeConfig
}
type IngressTLSConfig struct { Mode, Cert, Key string }
type IngressEdgeConfig struct { Provider string }
```

With:

```go
type IngressConfig struct {
    CloudflareManaged bool   `json:"cloudflare-managed,omitempty" yaml:"cloudflare-managed,omitempty"`
    Cert              string `json:"cert,omitempty" yaml:"cert,omitempty"`
    Key               string `json:"key,omitempty" yaml:"key,omitempty"`
}
```

Delete `IngressTLSConfig` and `IngressEdgeConfig`.

---

## Plan builder updates

### `internal/api/plan/plan.go`

**Ingress params derivation:**

Replace `desiredIngressExposure`, `desiredIngressTLSMode` and `ingressRouteParams` internals. New logic:

- `cloudflare-managed: true` → `exposure=edge_proxied, tls_mode=edge_origin, edge_provider=cloudflare`
- `cert` + `key` set → `tls_mode=provided, cert_pem=env[cert], key_pem=env[key]`
- neither → ACME default, no extra params

Delete `desiredIngressExposure` and `desiredIngressTLSMode` helper functions.

**Cron phases:**

Add `setCrons` after `setServices`:

```go
func setCrons(cfg *Cfg, env map[string]string) ([]Step, error) {
    var steps []Step
    for _, name := range utils.SortedKeys(cfg.Crons) {
        cron := cfg.Crons[name]
        params := map[string]any{
            "image": cron.Image, "schedule": cron.Schedule,
            "command": cron.Command, "server": cron.Server,
        }
        // resolve env, secrets, storage same as setServices
        steps = append(steps, Step{Kind: StepCronSet, Name: name, Params: params})
    }
    return steps, nil
}
```

Add `diffCrons` for removals (same pattern as `diffServices`).

**Managed service fields threading:**

`ResolveDeploymentSteps` must pass `Image`, `BackupStorage`, `BackupCron` from `cfg.Services[name]` into `managed.Request` when compiling managed bundles.

### `internal/api/plan/resolve.go`

Strip managed-owned crons from `cfg.Crons` before calling `Build()`, same as managed-owned services/volumes/storage are stripped.

---

## `--secret` behavior split between direct and cloud

### Direct CLI (DirectBackend)

`DatabaseSet` with `--secret POSTGRES_PASSWORD`:
1. Reads secret VALUE from cluster via `SecretReveal`
2. Passes value to `managed.Compile()` via env
3. Compiler validates, generates bundle
4. Executes operations immediately

### Cloud CLI (CloudBackend)

`DatabaseSet` with `--secret POSTGRES_PASSWORD`:
1. Does NOT read from cluster — no cluster at config time
2. Validates that `POSTGRES_PASSWORD` exists as a key in the pushed env
3. Sets `cfg.Services[name] = Service{Managed: kind, Secrets: ["POSTGRES_PASSWORD", ...], Image: ..., BackupStorage: ..., BackupCron: ...}`
4. Pushes config
5. At deploy time, `ResolveDeploymentSteps` passes the full env (which has the value) to the compiler

The `--secret` flags on `DatabaseSet` and `AgentSet` in `Backend` interface carry the KEY NAMES, not values. The direct backend resolves values from the cluster. The cloud backend validates keys exist in env. The compiler receives the resolved env either way.

This means `Backend.DatabaseSet` signature carries `opts.Secrets []string` — key names. Each backend handles resolution differently.

---

## Existing API endpoints — no changes needed

Already implemented, CloudBackend calls them directly:

```
GET  .../database                    → DatabaseList
POST .../database/:name/backup/create → BackupCreate  
GET  .../database/:name/backup       → BackupList
GET  .../database/:name/backup/:key  → BackupDownload
GET  .../agent                       → AgentList
POST .../agent/:name/exec           → AgentExec
GET  .../agent/:name/logs           → AgentLogs
GET  .../describe                    → DescribeCluster
GET  .../resources                   → ListResources
POST .../ssh                         → RunSSH
GET  .../services/:svc/logs          → ServiceLogs
POST .../services/:svc/exec          → ExecCommand
```

No new API endpoints required. The config mutation path uses the existing `POST /config` (push) endpoint.

---

## Backup image build — cloud path

Direct CLI builds the backup image on the server during `DatabaseSet` via `ensureBackupImage`. Cloud path doesn't — the executor needs to handle it.

Two options:

1. **Executor builds backup image during `service.set` step for managed postgres.** The executor already has SSH access. When it encounters a managed service with backup config, it builds the image before applying the cron.

2. **Separate `backup-image.build` step emitted by `ResolveDeploymentSteps`.** Explicit step in the plan. Executor dispatches it.

Option 2 is cleaner — explicit step, visible in the plan, same pattern as `build` for app images. Requires:

- New step kind `StepBackupImageBuild` in `plan.go`
- `ResolveDeploymentSteps` emits it before `StepCronSet` when managed service has backup config
- Executor dispatches: SSH to master, check registry, build if missing (same logic as `ensureBackupImage`)

---

## Summary

| Change | File |
|--------|------|
| Add Image/BackupStorage/BackupCron to Service schema | `config/schema.go` |
| Add Crons map to Config schema | `config/schema.go` |
| Replace IngressConfig with 3-field version | `config/schema.go` |
| Update validation | `config/validate.go` |
| Derive ingress params from CloudflareManaged bool | `plan/plan.go` |
| Add setCrons/diffCrons phases | `plan/plan.go` |
| Thread managed service fields to compiler | `plan/resolve.go` |
| Strip managed crons from Build() | `plan/resolve.go` |
| Add StepBackupImageBuild (optional) | `plan/plan.go` + `executor.go` |
| No new API endpoints | — |
