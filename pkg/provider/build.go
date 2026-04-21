package provider

import (
	"context"
	"fmt"
)

// BuildProvider is the substrate that physically executes `nvoi deploy`.
//
// Three implementations in the roadmap:
//
//   - local   — default; runs reconcile.Deploy in-process on the operator's
//     machine. Dispatch is never called (cmd/cli/deploy.go
//     shortcuts to reconcile.Deploy directly). Registered so
//     capability bits are addressable by name.
//   - ssh     — PR-B; SSHes to a server with `role: builder` and invokes
//     `nvoi deploy --local` there. Streams stdout/stderr back.
//   - daytona — future; dispatches into a sandbox runtime.
//
// The validator (ValidateConfig) queries capability bits by name — hence
// BuildCapability lives alongside the schema in the registry entry, not on
// an instance method. Resolving a provider requires credentials; the
// validator runs before credential resolution and must not need any.
//
// The deploy dispatcher in cmd/cli/deploy.go calls Dispatch for every
// BuildProvider whose name is not "local" (local is the in-process path).
// Providers that cannot be invoked from CI (e.g. local) set
// DispatchableFromCI = false so PR-C's ci validator can reject them.
type BuildProvider interface {
	// Dispatch executes a full deploy on whatever substrate this provider
	// represents. The remote side reads the operator's config (shipped via
	// BuildDispatch.ConfigPath) and the resolved environment
	// (BuildDispatch.Env) and invokes `nvoi deploy --local` there.
	//
	// Never called for the "local" provider — the CLI dispatches in-process
	// when the resolved name is "local".
	Dispatch(ctx context.Context, req BuildDispatch) error

	// Close releases any provider-internal resources (cached SSH
	// connections, HTTP transports, etc.). Idempotent.
	Close() error
}

// BuildDispatch is the input a remote BuildProvider needs to run a deploy
// on behalf of the operator.
type BuildDispatch struct {
	// ConfigPath is the absolute path on the operator's machine to the
	// nvoi.yaml the deploy is running against. Remote providers ship this
	// file to the builder; local never reads it (dispatch isn't called).
	ConfigPath string

	// Env is a snapshot of the operator's process environment at dispatch
	// time — every credential the in-process reconcile would have resolved
	// (infra / DNS / storage / registry / service $VAR expansion).
	// Remote providers stream this to the builder's shell env so
	// `nvoi deploy --local` on the builder resolves credentials the same
	// way the operator's laptop would have.
	//
	// Format matches os.Environ(): []string of "KEY=VALUE".
	Env []string

	// Sink is the operator's Output. Remote providers tunnel the builder's
	// stdout/stderr through this so the operator sees real-time progress
	// during a remote build. Defined as any to avoid coupling pkg/provider
	// to pkg/core — the local BuildProvider never uses it, and PR-B's ssh
	// provider will type-assert to a narrow progress interface.
	Sink any
}

// BuildCapability is the set of static, registration-time facts about a
// build provider that the validator and the CI resolver need. Held as
// data (not a method) because ValidateConfig runs before credentials are
// resolved, and methods on BuildProvider would require a live instance —
// which requires creds. These facts never vary at runtime for a given
// provider.
type BuildCapability struct {
	// RequiresBuilders reports whether this provider needs at least one
	// server with role: builder to execute. Validator rule R1:
	//   - RequiresBuilders == true  →  builders must be ≥ 1
	//   - RequiresBuilders == false →  builders must be == 0
	RequiresBuilders bool

	// DispatchableFromCI reports whether a CIProvider can dispatch deploys
	// to this build provider. Validator rule R2: providers.ci set + build
	// provider with DispatchableFromCI == false → error (e.g. `ci: github`
	// with `build: local` is meaningless — there's no remote to dispatch to).
	DispatchableFromCI bool
}

// ── Registry ─────────────────────────────────────────────────────────────────

type buildEntry struct {
	schema  CredentialSchema
	caps    BuildCapability
	factory func(creds map[string]string) BuildProvider
}

var buildProviders = map[string]buildEntry{}

// RegisterBuild registers a BuildProvider factory + capability under a name.
// Called from the provider package's init(). Re-registration replaces.
func RegisterBuild(name string, schema CredentialSchema, caps BuildCapability, factory func(creds map[string]string) BuildProvider) {
	buildProviders[name] = buildEntry{schema: schema, caps: caps, factory: factory}
}

// GetBuildSchema returns the credential schema for a build provider name.
// Used by cmd/cli/context.go when collecting creds for each configured
// provider, and by the validator when surfacing typos in providers.build.
func GetBuildSchema(name string) (CredentialSchema, error) {
	entry, ok := buildProviders[name]
	if !ok {
		return CredentialSchema{}, fmt.Errorf("unsupported build provider: %q", name)
	}
	return entry.schema, nil
}

// GetBuildCapability returns the static capability bits for a build
// provider name. The validator queries this during ValidateConfig —
// before credentials are resolved — so capabilities must be available
// without constructing a BuildProvider instance.
func GetBuildCapability(name string) (BuildCapability, error) {
	entry, ok := buildProviders[name]
	if !ok {
		return BuildCapability{}, fmt.Errorf("unsupported build provider: %q", name)
	}
	return entry.caps, nil
}

// ResolveBuild creates a BuildProvider with pre-resolved credentials.
// Credentials must already be fully resolved by the caller — same contract
// as ResolveInfra / ResolveDNS / ResolveBucket.
func ResolveBuild(name string, creds map[string]string) (BuildProvider, error) {
	entry, ok := buildProviders[name]
	if !ok {
		return nil, fmt.Errorf("unsupported build provider: %q", name)
	}
	if err := entry.schema.Validate(creds); err != nil {
		return nil, err
	}
	return entry.factory(creds), nil
}
