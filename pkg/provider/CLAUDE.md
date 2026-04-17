# CLAUDE.md — pkg/provider

Provider interfaces and credential resolution. Everything pluggable is a provider.

Three provider kinds live in core: `compute`, `dns`, `storage`. Anything else (secrets backends, build pipelines, database engines) is product-layer and must not land in core.

## Registration pattern

Same for all three kinds:

```go
provider.RegisterX("name", CredentialSchema{...}, func(creds map[string]string) XProvider {
    return New(creds)
})
```

1. **Interface** — `pkg/provider/{kind}.go` defines the interface
2. **Credential schema** — `pkg/provider/{impl}/register.go` declares required fields with env var mappings
3. **Registration** — `init()` calls `provider.RegisterX(name, schema, factory)`
4. **Blank import** — `cmd/cli/main.go` imports `_ "pkg/provider/{kind}/{impl}"` to trigger `init()`
5. **Resolution** — `provider.ResolveX(name, creds)` validates schema + returns instance

## Provider-owned operations

- **`ResolveDevicePath(vol) string`** on `ComputeProvider` — OS block device path for an attached volume. Hetzner returns `LinuxDevice`. AWS computes NVMe symlink.
- **`ListResources(ctx) ([]ResourceGroup, error)`** on all provider interfaces — returns every resource the provider created. `resources` command renders whatever comes back.
- **`RenderCloudInit(sshPublicKey, hostname)`** in `infra/` — cloud-init sets the hostname = k3s node name.

## Credential resolution

```go
// CredentialSource abstracts where credentials come from (env or in-memory map).
// ResolveFrom walks a schema and calls source.Get(field.EnvVar) for each field.
provider.ResolveFrom(schema, source) → map[string]string  // HETZNER_TOKEN lookup → token=xxx
```

At the cmd/ boundary `cmd/cli/context.go` builds an `EnvSource{}` and uses it for every provider credential lookup. No secrets provider bootstrap — that's a product-layer extension point.

**Region override:** `--compute-region` overrides `creds["region"]` after credential resolution.

## .env

Single file. Everything. Compose loads it via `env_file`.

```
# App identity
NVOI_APP_NAME=rails
NVOI_ENV=production

# Provider selection
COMPUTE_PROVIDER=aws          # hetzner | aws | scaleway
DNS_PROVIDER=cloudflare       # cloudflare | aws | scaleway
STORAGE_PROVIDER=aws          # cloudflare | aws | scaleway
DNS_ZONE=nvoi.to

# Provider credentials
HETZNER_TOKEN=...
AWS_ACCESS_KEY_ID=...
AWS_SECRET_ACCESS_KEY=...
AWS_REGION=eu-west-3
CF_API_KEY=...
CF_ACCOUNT_ID=...
CF_ZONE_ID=...
SSH_KEY_PATH=~/.ssh/id_ed25519

# App secrets (referenced by services via `secrets:`)
JWT_SECRET=...
ENCRYPTION_KEY=...
```
