package provider

import (
	"context"
	"fmt"
	"io"
)

// BuildProvider turns a BuildRequest into a pullable image reference.
//
// Each provider is self-contained. The interface does NOT prescribe how the
// image gets built or pushed — only that the returned ref is pullable by
// anyone who can authenticate to its registry. Internals vary wildly:
//
//   - local   — shells out to `docker buildx build --push` on the operator's
//     machine against the local docker daemon.
//   - ssh     — SSHes to a `role: builder` server and shells out
//     `docker buildx build --push` there (DOCKER_HOST=ssh://...).
//   - daytona — boots a Daytona sandbox, runs `docker buildx build --push`
//     inside it over the Daytona session-exec API.
//   - depot (future) — calls Depot's BuildKit gRPC endpoint; no docker CLI.
//   - buildpacks (future) — runs `pack build`, different tool entirely.
//
// The contract is deliberately thin. Providers share no architectural
// abstraction — only the return type. If three implementations end up with
// near-identical shell-out code, we extract a helper THEN, not now.
type BuildProvider interface {
	// Build ensures req.Image is pullable and returns the ref that actually
	// landed in the registry. Normally equals req.Image. A content-addressed
	// implementation may return a digest ref (e.g. `repo@sha256:...`) — the
	// caller stamps whatever is returned on the PodSpec.
	Build(ctx context.Context, req BuildRequest) (imageRef string, err error)

	// Close releases any provider-internal resources (cached SSH connections,
	// sandbox handles, HTTP transports, etc.). Idempotent.
	Close() error
}

// BuildRequest is everything a BuildProvider needs to produce one pullable
// image. The orchestrator (reconcile.BuildImages) populates one per service
// with a build: directive and calls Build.
type BuildRequest struct {
	// Service is the logical service name, used for logging only.
	Service string

	// Context is the absolute path to the build context on the operator's
	// filesystem. Providers that run the build remotely (ssh, daytona) are
	// responsible for transporting it to the remote — typically via
	// DOCKER_HOST=ssh://... which makes `docker buildx` handle context
	// upload natively.
	Context string

	// Dockerfile is the path to the Dockerfile. Relative or absolute per the
	// docker CLI's rules. When empty, the provider defaults to
	// <Context>/Dockerfile.
	Dockerfile string

	// Platform is the target architecture — "linux/amd64" or "linux/arm64".
	// Derived by the caller from infra.ArchForType(masterServerType).
	// Empty is a bug (caller failed to derive arch); providers should
	// refuse to build so a mismatched image never ships.
	Platform string

	// Image is the fully-qualified target ref: host/repo:tag-hash. Already
	// computed by reconcile.ResolveImage — providers tag and push to this
	// exact string.
	Image string

	// Registry is the push-side auth for the target registry. Host matches
	// the host prefix of Image. Values are post-credential-resolution — no
	// $VAR expansion left.
	Registry RegistryAuth

	// Builders is the set of role:builder servers currently provisioned for
	// this cluster, as reported by InfraProvider.BuilderTargets. Populated
	// by the orchestrator. Non-ssh providers ignore it; ssh picks the first
	// entry today (sharding is a future concern).
	Builders []BuilderTarget

	// SSHKey is the operator's SSH private key bytes — same material the
	// rest of reconcile uses to dial cluster nodes. Non-nil only when
	// Builders is non-empty. ssh authenticates against role:builder with
	// this; other providers ignore it.
	SSHKey []byte

	// GitRemote is the upstream URL of the operator's current checkout —
	// inferred by the CLI via `git remote get-url origin` at deploy time,
	// NOT declared in nvoi.yaml. Remote builders (ssh, daytona) `git clone`
	// it on the build host because they can't reach the operator's
	// filesystem. Local ignores it (it already has Context on disk).
	//
	// Empty string when the operator's cwd is not a git checkout —
	// providers that require it (ssh, daytona) error with an actionable
	// message pointing at the fix ("initialize a git repo, add an origin
	// remote").
	GitRemote string

	// GitRef is the commit SHA of HEAD at deploy time (`git rev-parse HEAD`).
	// Pinned to a SHA rather than a branch so the remote builder clones the
	// exact tree the operator is deploying, even if `main` advances between
	// the CLI invocation and the clone. Local ignores it.
	GitRef string

	// Output receives build logs (docker buildx streams, sandbox session
	// output, etc.). Providers write build progress here so the operator
	// sees real-time build output during reconcile.
	Output io.Writer
}

// RegistryAuth is the host+credentials for one registry. Provider-agnostic:
// the local/ssh/daytona runners docker-login with it; depot feeds it into
// its gRPC request; buildpacks passes it to pack.
type RegistryAuth struct {
	Host     string
	Username string
	Password string
}

// BuildCapability is the set of static, registration-time facts about a
// build provider that the validator needs. Held as data (not a method)
// because ValidateConfig runs before credentials are resolved, and methods
// on BuildProvider would require a live instance — which requires creds.
// These facts never vary at runtime for a given provider.
type BuildCapability struct {
	// RequiresBuilders reports whether this provider needs at least one
	// server with role: builder to execute. Validator rule:
	//   - RequiresBuilders == true  →  builders must be ≥ 1
	//   - RequiresBuilders == false →  builders must be == 0
	RequiresBuilders bool
}

// ── Registry ─────────────────────────────────────────────────────────────────
//
// Build is the one outlier among the seven provider kinds: alongside the
// usual schema + factory, each entry carries a static BuildCapability
// read BEFORE credentials are resolved (see GetBuildCapability). Rather
// than teach the generic registry a second payload slot (cost: a second
// type param on six kinds that never use it), we keep a tiny parallel
// map here. Capabilities are registration-time data, never per-instance,
// so a parallel map is the simplest fit.

var (
	buildRegistry = newRegistry[BuildProvider]("build")
	buildCaps     = map[string]BuildCapability{}
)

// RegisterBuild registers a BuildProvider factory + capability under a name.
// Called from the provider package's init(). Re-registration replaces.
func RegisterBuild(name string, schema CredentialSchema, caps BuildCapability, factory func(creds map[string]string) BuildProvider) {
	buildRegistry.register(name, schema, factory)
	buildCaps[name] = caps
}

// GetBuildSchema returns the credential schema for a build provider name.
// Used by cmd/cli/context.go when collecting creds for each configured
// provider, and by the validator when surfacing typos in providers.build.
func GetBuildSchema(name string) (CredentialSchema, error) {
	return buildRegistry.getSchema(name)
}

// GetBuildCapability returns the static capability bits for a build
// provider name. The validator queries this during ValidateConfig —
// before credentials are resolved — so capabilities must be available
// without constructing a BuildProvider instance.
func GetBuildCapability(name string) (BuildCapability, error) {
	caps, ok := buildCaps[name]
	if !ok {
		return BuildCapability{}, fmt.Errorf("unsupported build provider: %q", name)
	}
	return caps, nil
}

// ResolveBuild creates a BuildProvider with pre-resolved credentials.
// Credentials must already be fully resolved by the caller — same contract
// as ResolveInfra / ResolveDNS / ResolveBucket.
func ResolveBuild(name string, creds map[string]string) (BuildProvider, error) {
	return buildRegistry.resolve(name, creds)
}
