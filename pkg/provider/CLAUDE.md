# CLAUDE.md — pkg/provider

Provider interfaces and credential resolution. Everything pluggable is a provider.

Four provider kinds live in core: `compute`, `dns`, `storage`, `secrets`. Build pipelines and database engines stay product-layer and do not land in core — `build:` on a service is a local shell-out to `docker buildx`, not a provider plugin.

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

At the cmd/ boundary `cmd/cli/context.go` calls `credentialSource(ctx, cfg)` which returns either:

- `EnvSource{}` — default, when `providers.secrets` is unset in `nvoi.yaml`. Every credential comes from `os.Getenv`.
- `SecretsSource{Ctx, Provider}` — when `providers.secrets` is set to `doppler | awssm | infisical` (scalar, same shape as the other providers; struct form `{kind: ...}` is also accepted for forward compat). The backend's own creds bootstrap from env (the escape hatch), then every downstream credential — compute, DNS, storage, SSH key, service `$VAR` expansion — is fetched from the backend at deploy time. `ValidateCredentials` runs at startup so a misconfigured backend fails loudly, not mid-deploy.

**Adapters are direct-API, never shell-outs.** Doppler via REST (`utils.HTTPClient` + Bearer). AWS Secrets Manager via `aws-sdk-go-v2`. Infisical via REST Universal Auth (`client_id` + `client_secret` → access token), cloud and self-hosted.

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
