# CloudBackend config-mutation implementation

Every stubbed method in `internal/cli/stubs.go` must be implemented. No stubs. No error placeholders.

## How it works

The cloud API stores config + env in `RepoConfig` rows (see `internal/api/models.go`):

```
RepoConfig
  ID              string (UUID)
  RepoID          string (FK)
  Version         int (auto-incremented per push)
  ComputeProvider enum (hetzner | aws | scaleway)
  DNSProvider     enum (cloudflare | aws)
  StorageProvider enum (cloudflare | aws)
  BuildProvider   enum (local | daytona | github)
  Config          text (YAML string — the config we mutate)
  Env             text (encrypted KEY=VALUE pairs — secrets + credentials)
```

Each imperative command does:
1. `GET /config?reveal=true` → load latest RepoConfig (YAML + decrypted env)
2. Parse YAML into `config.Config` struct
3. Mutate the struct (add server, add service, etc.)
4. Marshal back to YAML
5. `POST /config` with YAML + env + provider enums → creates new version

State accumulates: `instance set` pushes version 1. `service set` loads version 1, adds service, pushes version 2. `deploy` reads version 2 with both.

`deploy` triggers `ResolveDeploymentSteps` which compiles managed bundles, plans non-managed resources, creates deployment steps, and the executor runs them.

---

## Pattern

Every config-mutation method follows this exact pattern:

```go
func (c *CloudBackend) XXXSet(_ context.Context, ...) error {
    cfg, env, err := c.loadConfig()
    if err != nil { return err }
    // mutate cfg
    return c.pushConfig(cfg, env)
}
```

---

## Required helpers in `internal/cli/stubs.go` (or rename to `config_mutation.go`)

### loadConfig

```go
func (c *CloudBackend) loadConfig() (*config.Config, string, error)
```

- `GET /workspaces/{wsID}/repos/{repoID}/config?reveal=true`
- Parse the YAML config string from the response
- Parse the env string from the response
- If 404 (no config yet), return empty `Config{Servers: {}, Services: {}}` and empty env
- Return `(*config.Config, envString, error)`

### pushConfig

```go
func (c *CloudBackend) pushConfig(cfg *config.Config, env string) error
```

- Marshal config to YAML via `sigs.k8s.io/yaml`
- `POST /workspaces/{wsID}/repos/{repoID}/config` with body:
  ```json
  {
    "config": "<yaml string>",
    "env": "<env string>",
    "compute_provider": os.Getenv("COMPUTE_PROVIDER"),
    "dns_provider": os.Getenv("DNS_PROVIDER"),
    "storage_provider": os.Getenv("STORAGE_PROVIDER"),
    "build_provider": os.Getenv("BUILD_PROVIDER")
  }
  ```
- Provider values come from OS env (compose injects them from `examples/.env`)

---

## Every method — exact implementation

### InstanceSet

```go
cfg.Servers[name] = config.Server{Type: serverType, Region: region}
```

Init `cfg.Servers` if nil.

### InstanceDelete

```go
delete(cfg.Servers, name)
```

### FirewallSet

```go
cfg.Firewall = &config.FirewallConfig{Preset: args[0]}
```

`args` is the slice from the Backend interface. First element is the preset name.

### VolumeSet

```go
cfg.Volumes[name] = config.Volume{Size: size, Server: server}
```

Init `cfg.Volumes` if nil.

### VolumeDelete

```go
delete(cfg.Volumes, name)
```

### StorageSet

```go
cfg.Storage[name] = config.Storage{CORS: cors, ExpireDays: expireDays, Bucket: bucket}
```

Init `cfg.Storage` if nil.

### StorageDelete

```go
delete(cfg.Storage, name)
```

### ServiceSet

```go
cfg.Services[name] = config.Service{
    Image:    opts.Image,
    Build:    opts.Build,
    Port:     opts.Port,
    Replicas: opts.Replicas,
    Command:  opts.Command,
    Server:   opts.Server,
    Health:   opts.Health,
    Env:      opts.Env,
    Secrets:  opts.Secrets,
    Storage:  opts.Storage,
    Volumes:  opts.Volumes,
}
```

Init `cfg.Services` if nil.

### ServiceDelete

```go
delete(cfg.Services, name)
```

### DatabaseSet

```go
cfg.Services[name] = config.Service{
    Managed: opts.Kind,
    Secrets: opts.Secrets,
}
```

Init `cfg.Services` if nil. The `--secret` flags carry KEY NAMES (not values). The values are already in the pushed env. The compiler reads them at deploy time.

### DatabaseDelete

```go
delete(cfg.Services, name)
```

### AgentSet

```go
cfg.Services[name] = config.Service{
    Managed: opts.Kind,
    Secrets: opts.Secrets,
}
```

### AgentDelete

```go
delete(cfg.Services, name)
```

### SecretSet

Secrets go into the env string, not the config.

```go
cfg, env, err := c.loadConfig()
// Parse env as KEY=VALUE lines
// If key exists, replace the line
// If key doesn't exist, append "KEY=VALUE"
// pushConfig with updated env
```

### SecretDelete

```go
cfg, env, err := c.loadConfig()
// Parse env as KEY=VALUE lines
// Filter out the line where key matches
// pushConfig with filtered env
```

### DNSSet

```go
for _, route := range routes {
    cfg.Domains[route.Service] = config.Domains(route.Domains)
}
```

Init `cfg.Domains` if nil.

If `cloudflareManaged` is true, also set ingress to cloudflare-managed mode. Use the CURRENT schema fields (this will be simplified when the ingress schema cleanup lands):

```go
cfg.Ingress = &config.IngressConfig{
    Exposure: "edge_proxied",
    TLS:      &config.IngressTLSConfig{Mode: "edge_origin"},
    Edge:     &config.IngressEdgeConfig{Provider: "cloudflare"},
}
```

### DNSDelete

```go
for _, route := range routes {
    delete(cfg.Domains, route.Service)
}
```

### IngressSet

If `cloudflareManaged`:
```go
cfg.Ingress = &config.IngressConfig{
    Exposure: "edge_proxied",
    TLS:      &config.IngressTLSConfig{Mode: "edge_origin"},
    Edge:     &config.IngressEdgeConfig{Provider: "cloudflare"},
}
```

If `certPEM` and `keyPEM` provided:
```go
cfg.Ingress = &config.IngressConfig{
    TLS: &config.IngressTLSConfig{Mode: "provided", Cert: certPEM, Key: keyPEM},
}
```

Neither: `cfg.Ingress = nil` (ACME default).

### IngressDelete

```go
cfg.Ingress = nil
```

### Build

```go
for _, target := range opts.Targets {
    // parse "name:source" from target string
    name, source := parseBuildTarget(target)
    cfg.Build[name] = config.Build{Source: source}
}
```

Init `cfg.Build` if nil. `opts.Targets` is `[]string` with format `"name:source"`.

### CronSet

Crons are NOT in the config schema yet. This is tracked in `_todo/api.md`. For now, return a clear error:

```go
return fmt.Errorf("cron set via cloud not yet supported — requires Crons map in config schema (tracked in _todo/api.md)")
```

### CronDelete

Same:
```go
return fmt.Errorf("cron delete via cloud not yet supported — requires Crons map in config schema")
```

---

## Delete the configMutation placeholder

Remove from `internal/cli/backend.go`:
```go
func (c *CloudBackend) configMutation() error {
    return fmt.Errorf("not yet available in cloud mode — use 'nvoi push' + 'nvoi deploy'")
}
```

It is no longer needed. Every method has a real implementation.

---

## Nil map safety

Every method that adds to a map must check for nil first:

```go
if cfg.Servers == nil { cfg.Servers = map[string]config.Server{} }
if cfg.Services == nil { cfg.Services = map[string]config.Service{} }
if cfg.Volumes == nil { cfg.Volumes = map[string]config.Volume{} }
if cfg.Storage == nil { cfg.Storage = map[string]config.Storage{} }
if cfg.Build == nil { cfg.Build = map[string]config.Build{} }
if cfg.Domains == nil { cfg.Domains = map[string]config.Domains{} }
```

---

## Verification

After implementing, this must work:

```bash
bin/cloud login
bin/cloud repos use dummy-rails
bin/cloud instance set master --compute-type cx23 --compute-region fsn1
bin/cloud secret set POSTGRES_PASSWORD $(openssl rand -hex 16)
bin/cloud database set db --type postgres --secret POSTGRES_PASSWORD --secret POSTGRES_USER --secret POSTGRES_DB
bin/cloud plan
```

`bin/cloud plan` must show the deployment steps generated from the accumulated config. If it doesn't, the implementation is wrong.
