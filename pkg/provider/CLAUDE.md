# CLAUDE.md — pkg/provider

Provider interfaces and credential resolution. Everything pluggable is a provider.

Seven provider kinds live in core: `infra`, `dns`, `storage`, `secrets`, `tunnel`, `build`, `ci`. (Pre-#47 the first kind was named `compute` and exposed a 16-method `ComputeProvider` interface mixing IaaS-specific ops with what nvoi actually needed. C10 deleted it; `InfraProvider` is the narrow replacement — `Bootstrap → *kube.Client` is the single load-bearing promise.) The `build` kind (added in #56-A) is the outer substrate a deploy runs on — `local` (default, in-process), `ssh` (PR-B, dispatches to a `role: builder` server), `daytona` (PR-C, dispatches into a managed sandbox). Distinct from the inner docker-buildx-and-push step in `internal/reconcile/images.go::BuildImages`, which runs pre-infra on whichever machine is executing `reconcile.Deploy`. The `ci` kind (added in #61) is consumed ONLY by `nvoi ci init`; `reconcile.Deploy` never reads `providers.ci`. Today: `github` (GitHub Actions). Database engines stay product-layer and do not land in core.

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

## CIProvider

`pkg/provider/ci.go` + `pkg/provider/github/`. Four methods, zero cluster touch:

- **`ValidateCredentials(ctx) error`** — cheap `/user` + repo-existence probe. Runs before any mutation so a bad token / wrong repo fails fast.
- **`Target() CITarget`** — `{URL, Owner, Repo}` for operator-facing log lines.
- **`SyncSecrets(ctx, map[string]string) error`** — upload every secret to the provider's secret store. GitHub path: fetches `/actions/secrets/public-key`, seals each value via `golang.org/x/crypto/nacl/box.SealAnonymous` (curve25519 / libsodium sealed-box), PUTs to `/actions/secrets/{name}`. Empty values are rejected at the collector (`cmd/cli/ci.go::collectCISecrets`) — an empty secret on the runner reads as "absent" and breaks `ValidateConfig` obscurely.
- **`RenderWorkflow(CIWorkflowPlan) (path, content, error)`** — deterministic workflow file. `SecretEnv` sorted by the caller so re-runs are byte-identical; the Contents API diff stays quiet when nothing changed.
- **`CommitFiles(ctx, []CIFile, message) (url, error)`** — direct push to the default branch when it accepts direct pushes, feature branch (`nvoi/ci-init`) + PR otherwise. Protection detection: rulesets-first, then classic branch protection. 403-on-list treated as protected (safer default than half-committing a push that a hidden rule then rejects). 422 "repository rule violations" body-sniffed as an inline fallback trigger. Idempotent — existing PR reused on re-run.

Credentials: `GITHUB_TOKEN` (fine-grained PAT or OAuth) + `GITHUB_REPO` (`owner/repo` | full URL). `GITHUB_REPO` auto-inferred from `git remote get-url origin` at the `cmd/` boundary when unset — the git CLI shell-out lives ONLY in `cmd/cli/ci.go`, never in the provider.

Hosted-runner model only (`ubuntu-latest`). The rendered workflow curls `cdn.nvoi.to/bin/<version>/nvoi` to pin the runner binary.

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
