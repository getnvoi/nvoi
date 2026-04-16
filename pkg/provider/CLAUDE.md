# CLAUDE.md — pkg/provider

Provider interfaces and credential resolution. Everything pluggable is a provider.

## Registration pattern

Same for all four kinds:

```go
provider.RegisterX("name", CredentialSchema{...}, func(creds map[string]string) XProvider {
    return New(creds)
})
```

1. **Interface** — `pkg/provider/{kind}.go` defines the interface
2. **Credential schema** — `pkg/provider/{impl}/register.go` declares required fields with env var mappings
3. **Registration** — `init()` calls `provider.RegisterX(name, schema, factory)`
4. **Blank import** — `internal/core/{command}.go` imports `_ "pkg/provider/{impl}"` to trigger `init()`
5. **Resolution** — `provider.ResolveX(name, creds)` validates schema + returns instance

## Provider-owned operations

- **`ResolveDevicePath(vol) string`** on `ComputeProvider` — OS block device path for an attached volume. Hetzner returns `LinuxDevice`. AWS computes NVMe symlink.

- **`ListResources(ctx) ([]ResourceGroup, error)`** on all provider interfaces — returns every resource the provider created. `resources` command renders whatever comes back.

- **`RenderCloudInit(sshPublicKey, hostname)`** in `infra/` — cloud-init sets the hostname = k3s node name.

## Credential resolution

Two layers:

```go
// CredentialSource abstracts where credentials come from (env, map, secrets provider).
// ResolveFrom walks a schema and calls source.Get(field.EnvVar) for each field.
provider.ResolveFrom(schema, source) → map[string]string  // HETZNER_TOKEN lookup → token=xxx
```

At the cmd/ boundary: if `providers.secrets` is set, bootstrap that provider from `EnvSource{}`, then use `SecretsSource{}` for everything else. Otherwise `EnvSource{}`. See `cmd/cli/context.go:credentialSource`.

**Region override:** `--compute-region` overrides `creds["region"]` after credential resolution.

## Credential pairs

Every provider has a name flag + credentials flag. Always a pair.

```bash
# Common: env vars set
bin/core instance set master --compute-type cx23 --compute-region fsn1

# Override credentials
bin/core instance set master \
  --compute-provider hetzner \
  --compute-credentials HETZNER_TOKEN=$OTHER_TOKEN \
  --compute-type cx23 --compute-region fsn1

# Build uses two providers
bin/core build \
  --compute-provider hetzner --compute-credentials HETZNER_TOKEN=xxx \
  --build-provider daytona --build-credentials api_key=xxx \
  --source myorg/app --name web

# Error when missing
# hetzner: token is required (--compute-credentials HETZNER_TOKEN=..., env: HETZNER_TOKEN)
```

## .env

Single file. Everything. Compose loads it via `env_file`.

```
# App identity
NVOI_APP_NAME=rails
NVOI_ENV=production

# Provider selection
COMPUTE_PROVIDER=aws          # hetzner | aws | scaleway
DNS_PROVIDER=cloudflare       # cloudflare | aws | scaleway
STORAGE_PROVIDER=aws          # cloudflare | aws
BUILD_PROVIDER=daytona        # local | daytona | github
DNS_ZONE=nvoi.to

# Provider credentials
HETZNER_TOKEN=...
AWS_ACCESS_KEY_ID=...
AWS_SECRET_ACCESS_KEY=...
AWS_REGION=eu-west-3
CF_API_KEY=...
CF_ACCOUNT_ID=...
CF_ZONE_ID=...
DAYTONA_API_KEY=...
SSH_KEY_PATH=~/.ssh/id_ed25519

# App secrets
POSTGRES_USER=...
POSTGRES_PASSWORD=...
POSTGRES_DB=...
RAILS_MASTER_KEY=...
```
