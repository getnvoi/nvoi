# CLAUDE.md — pkg/provider

Provider interfaces, credential schemas, and credential resolution. Everything pluggable is a provider.

## Registration pattern

Same for all five kinds (compute, dns, storage, build, secrets):

```go
provider.RegisterX("name", CredentialSchema{...}, func(creds map[string]string) XProvider {
    return New(creds)
})
```

1. **Interface** — `pkg/provider/{kind}.go` defines the interface
2. **Credential schema** — `pkg/provider/{impl}/register.go` declares required fields with env var mappings
3. **Registration** — `init()` calls `provider.RegisterX(name, schema, factory)`
4. **Blank import** — `cmd/cli/main.go` and `cmd/api/main.go` import `_ "pkg/provider/{impl}"` to trigger `init()`
5. **Resolution** — `provider.ResolveX(name, creds)` validates schema + returns instance

## CredentialSource

Single abstraction for where credential values come from:

- `EnvSource` — `os.Getenv` (CLI, no secrets provider)
- `SecretsSource` — external secrets provider (Infisical, Doppler, AWS SM)
- `MapSource` — in-memory map (tests)

`cmd/` selects the source. `pkg/` and `internal/` call `source.Get(key)` — never branch on source type.

`ResolveFrom(schema, source)` iterates schema fields, calls `source.Get(field.EnvVar)` for each.

## Provider-owned operations

- **`ResolveDevicePath(vol) string`** on `ComputeProvider` — OS block device path for an attached volume. Hetzner returns `LinuxDevice`. AWS computes NVMe symlink.
- **`ListResources(ctx) ([]ResourceGroup, error)`** on all provider interfaces — returns every resource the provider created. `resources` command renders whatever comes back.
- **`RenderCloudInit(sshPublicKey, hostname)`** in `infra/` — cloud-init sets the hostname = k3s node name.

## SecretsProvider

Read-only. `Get(ctx, key)` and `List(ctx)` only. nvoi never writes secrets. `Get()` returns `("", nil)` for absent keys — errors are for real failures (auth, network). Three implementations: doppler, awssm, infisical.
