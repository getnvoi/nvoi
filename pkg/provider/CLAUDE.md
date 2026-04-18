# CLAUDE.md — pkg/provider

Provider interfaces and credential resolution. Everything pluggable is a provider.

Four provider kinds live in core: `infra`, `dns`, `storage`, `secrets`. (Pre-#47 the first kind was named `compute` and exposed a 16-method `ComputeProvider` interface mixing IaaS-specific ops with what nvoi actually needed. C10 deleted it; `InfraProvider` is the narrow replacement — `Bootstrap → *kube.Client` is the single load-bearing promise.) Build pipelines and database engines stay product-layer and do not land in core — `build:` on a service is a local shell-out to `docker buildx`, not a provider plugin.

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

- **`InfraProvider.Connect(ctx, dc) (*kube.Client, error)`** — READ-ONLY attach. Looks up existing infra, dials SSH, builds the kube tunnel. Returns `provider.ErrNotBootstrapped` when no infra is found (callers distinguish via `errors.Is`). MUST NOT mutate any provider resource. Cost target: ≤500 ms on an existing Hetzner cluster. Called by `Cluster.Kube` on the CLI dispatch path (every `nvoi` command except `deploy` / `teardown`).
- **`InfraProvider.Bootstrap(ctx, dc) (*kube.Client, error)`** — WRITE. Converges infra to the desired state (creates missing servers, reconciles firewall rules, applies node labels, installs k3s) and tail-calls `Connect`. Idempotent on existing resources but drift IS reconciled. Called only by `reconcile.Deploy`.
- **`InfraProvider.NodeShell(ctx, dc) (utils.SSHClient, error)`** — host shell for `nvoi ssh`. Returns `(nil, nil)` for backends without one (managed k8s); CLI feature-gates on nil.
- **`InfraProvider.IngressBinding(ctx, dc, svc) (IngressBinding, error)`** — DNS hint + target. IaaS: `{DNSType:"A", DNSTarget: master.IPv4}`. Managed: `{DNSType:"CNAME", DNSTarget: lb.hostname}`. DNSProvider picks the actual record kind.
- **`InfraProvider.HasPublicIngress() bool`** + **`ConsumesBlocks() []string`** — gates the validator + reconcile use to avoid per-provider branching.
- **`ListResources(ctx) ([]ResourceGroup, error)`** on every provider interface — returns every resource the provider created. `resources` command renders whatever comes back.
- **`RenderCloudInit(sshPublicKey, hostname)`** in `pkg/infra/` — cloud-init sets the hostname = k3s node name. Called from each IaaS provider's `provisionServer` helper.

## On-demand connect contract

`Cluster.Kube(ctx, cfg)` and `Cluster.SSH(ctx, cfg)` route to `infra.Connect` / `infra.NodeShell` when their fields (`MasterKube` / `NodeShell`) are nil. `Connect` is read-only (lookup + SSH dial + kube tunnel build); CLI dispatch pays ≤500 ms on existing clusters. Drift reconciliation lives in `Bootstrap` and only `reconcile.Deploy` calls it. Tests NEVER pre-inject `Cluster.MasterKube` or `Cluster.NodeShell` — the on-demand path is mandatory coverage. Acceptance gate:

```
grep -rE 'Cluster\{[^}]*MasterKube|Cluster\.MasterKube\s*=|Cluster\{[^}]*NodeShell|Cluster\.NodeShell\s*=' \
    cmd/ pkg/core/ | grep '_test\.go'   # → zero hits
```

The Connect/Bootstrap split fixes the production failure class where `nvoi logs` could silently reconcile firewall drift and lock the user out — `cmd/cli/dispatch_test.go::TestDispatch_DriftScenario_DoesNotReconcile` locks the contract.

## Credential resolution

```go
// CredentialSource abstracts where credentials come from (env or in-memory map).
// ResolveFrom walks a schema and calls source.Get(field.EnvVar) for each field.
provider.ResolveFrom(schema, source) → map[string]string  // HETZNER_TOKEN lookup → token=xxx
```

At the cmd/ boundary `cmd/cli/context.go` calls `credentialSource(ctx, cfg)` which returns either:

- `EnvSource{}` — default, when `providers.secrets` is unset in `nvoi.yaml`. Every credential comes from `os.Getenv`.
- `SecretsSource{Ctx, Provider}` — when `providers.secrets` is set to `doppler | awssm | infisical` (scalar, same shape as the other providers; struct form `{kind: ...}` is also accepted for forward compat). The backend's own creds bootstrap from env (the escape hatch), then every downstream credential — infra, DNS, storage, SSH key, service `$VAR` expansion — is fetched from the backend at deploy time. `ValidateCredentials` runs at startup so a misconfigured backend fails loudly, not mid-deploy.

**Adapters are direct-API, never shell-outs.** Doppler via REST (`utils.HTTPClient` + Bearer). AWS Secrets Manager via `aws-sdk-go-v2`. Infisical via REST Universal Auth (`client_id` + `client_secret` → access token), cloud and self-hosted.

**Region override:** `--infra-region` overrides `creds["region"]` after credential resolution.

## .env

Single file. Everything. Compose loads it via `env_file`.

```
# App identity
NVOI_APP_NAME=rails
NVOI_ENV=production

# Provider selection
INFRA_PROVIDER=aws            # hetzner | aws | scaleway
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
