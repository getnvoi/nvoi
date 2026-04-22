package provider

import (
	"context"
)

// CIProvider dispatches `nvoi deploy` from a CI substrate (GitHub Actions
// today; GitLab CI / Bitbucket Pipelines tomorrow). The goal is non-custody
// SaaS-mode onboarding: every credential the operator holds in their local
// env or .env gets moved to the CI provider's own secret store, a workflow
// that runs `nvoi deploy` is committed to the repo, and from that point
// forward `git push` is the deploy. The operator's laptop never holds
// prod secrets again.
//
// `providers.ci` is read by exactly one command: `nvoi ci init`. It is NOT
// read by `reconcile.Deploy` — the deploy engine is the same whether it
// runs on a laptop or a CI runner. The field is configuration metadata
// for onboarding, not a runtime branch.
//
// Every CIProvider is entirely self-contained. The only shared contract is
// this interface — no shared transport, no shared workflow template, no
// shared git mechanics. GitHub uses the REST API end-to-end (no git CLI
// dependency on the operator's machine); other providers can shell out if
// their SDKs demand it.
type CIProvider interface {
	// ValidateCredentials probes the provider (e.g. GET /user on GitHub) so
	// a bad token fails at `nvoi ci init` startup, not halfway through the
	// secret sync. Runs once per invocation.
	ValidateCredentials(ctx context.Context) error

	// Target returns the repo/project coordinates this provider is wired
	// to. Derived by the provider from the Git remote URL passed at
	// construction (via `providers.ci` credentials, typically GITHUB_REPO
	// or inferred from `git remote get-url origin`). Printed before any
	// write so the operator sees which repo is about to be touched.
	Target() CITarget

	// SyncSecrets uploads the given name→value map to the provider's secret
	// store for the target repo/project. Idempotent — overwrites existing
	// secrets of the same name. Never reads existing secret values back
	// (providers don't expose ciphertext read-back in their APIs anyway).
	// Empty map is a no-op, not an error.
	SyncSecrets(ctx context.Context, secrets map[string]string) error

	// RenderWorkflow returns the on-disk relative path and bytes of the
	// provider-native workflow file:
	//   - GitHub     → .github/workflows/nvoi.yml
	//   - GitLab     → .gitlab-ci.yml
	//   - Bitbucket  → bitbucket-pipelines.yml
	// Pure function of the plan — no network, no git, no I/O. Called by
	// the CLI for both the commit path (writes bytes) and the preview path
	// (prints bytes without writing).
	RenderWorkflow(plan CIWorkflowPlan) (path string, content []byte, err error)

	// CommitFiles writes the given files to the target repo. When the
	// default branch accepts direct commits, pushes there. When branch
	// protection or repository rulesets block a direct push, creates a
	// feature branch (nvoi/ci-init) and opens a PR. Returns a URL the CLI
	// prints — either the commit URL (direct push) or the PR URL
	// (protected branch).
	//
	// Idempotent: re-running overwrites existing files with the same path.
	// The commit message is the same each run; no chronological history
	// gets polluted by repeated `ci init` invocations.
	CommitFiles(ctx context.Context, files []CIFile, commitMessage string) (url string, err error)

	// ListResources returns every provider-side artifact `nvoi ci init`
	// created (secrets, workflow files, open PRs). Used by `nvoi resources`
	// to surface the SaaS-mode state alongside infra resources.
	ListResources(ctx context.Context) ([]ResourceGroup, error)

	// Close releases any provider-internal resources (HTTP transports,
	// token caches). Idempotent.
	Close() error
}

// CITarget is the repo/project coordinates a CIProvider is wired to.
type CITarget struct {
	Kind  string // "github", "gitlab", "bitbucket"
	Owner string // org or user
	Repo  string // repository slug
	URL   string // browser URL to the repo root
}

// CIWorkflowPlan is the input to RenderWorkflow. Small and stable — any
// per-provider knobs stay internal to the provider's own factory.
type CIWorkflowPlan struct {
	// NvoiVersion is the pinned nvoi binary tag the workflow downloads in
	// the runner. Empty string → the workflow uses `latest` (not
	// recommended for prod, flagged by the validator).
	NvoiVersion string

	// SecretEnv is every secret name the workflow must expose as an env
	// var on the `nvoi deploy` step. Ordered — the provider renders them
	// in this exact order so the workflow diff is deterministic across
	// repeated runs.
	SecretEnv []string
}

// CIFile is one file to commit during `ci init`. Path is relative to the
// repo root; Content is UTF-8 bytes.
type CIFile struct {
	Path    string
	Content []byte
}

// ── Registry ─────────────────────────────────────────────────────────────────

var ciRegistry = newRegistry[CIProvider]("CI")

// RegisterCI registers a CIProvider factory under a name. Called from the
// provider package's init(). Re-registration replaces.
func RegisterCI(name string, schema CredentialSchema, factory func(creds map[string]string) CIProvider) {
	ciRegistry.register(name, schema, factory)
}

// GetCISchema returns the credential schema for a CI provider name. Used
// by cmd/cli/context.go when resolving credentials and by the validator
// when surfacing typos in providers.ci.
func GetCISchema(name string) (CredentialSchema, error) {
	return ciRegistry.getSchema(name)
}

// ResolveCI creates a CIProvider with pre-resolved credentials. Same
// contract as ResolveInfra / ResolveBuild — credentials must already be
// fully resolved by the caller.
func ResolveCI(name string, creds map[string]string) (CIProvider, error) {
	return ciRegistry.resolve(name, creds)
}
