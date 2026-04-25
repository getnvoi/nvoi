// Package ssh is the "providers.build: ssh" BuildProvider. It builds one
// service's image on a `role: builder` server reached over SSH with the
// operator's private key.
//
// Mechanics on Build:
//
//  1. SSH-dial the first BuildRequest.Builders entry as User with
//     BuildRequest.SSHKey (TOFU host keys + auth-failure sentinels via
//     pkg/infra.ConnectSSH).
//  2. Allocate a per-build workspace on the builder (`mktemp -d`).
//  3. `git clone` the operator's checkout remote (BuildRequest.GitRemote)
//     at the exact commit the operator is deploying (BuildRequest.GitRef).
//     Shallow clone + fetch-SHA so the builder pulls the smallest possible
//     payload. Private-repo auth is the caller's problem — the CLI is
//     expected to have rewritten the URL to carry a token when needed
//     (e.g. `https://x-access-token:<TOKEN>@github.com/...`). We do not
//     read GITHUB_TOKEN here; credential boundaries belong in cmd/.
//  4. `docker login` the push-side registry with --password-stdin so the
//     password never touches argv / ps / shell history on the builder.
//  5. `docker buildx build --push --platform <P> -t <Image> -f <Dockerfile>
//     <workspace>/<Context>`. Streams stdout/stderr back to the operator's
//     BuildRequest.Output so `nvoi deploy` shows real-time remote progress.
//  6. Best-effort `rm -rf` of the workspace. Cache (Docker's BuildKit
//     layer store on the cache volume) persists across builds and is the
//     source of the speed win; the workspace itself is ephemeral.
//
// Daytona-shape, SSH-adapted: same "clone + login + buildx --push" pattern
// the 9b39daf daytona provider used, with SSH as the transport instead of
// a managed sandbox's session-exec API.
//
// Credentials: none. The SSH private key and builder addresses both ride
// on BuildRequest (populated by the orchestrator from the operator's
// resolved SSHKey + the InfraProvider's BuilderTargets). Same pattern as
// NodeShell — the provider owns nothing of its own, it's a transport.
package ssh

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/getnvoi/nvoi/pkg/infra"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// dialFunc is the SSH dial seam. Production: infra.ConnectSSH. Tests inject
// a closure returning a MockSSH without touching TCP. Same shape as the
// BootstrapContext.SSHDial hook the IaaS providers use.
type dialFunc func(ctx context.Context, addr, user string, privateKey []byte) (utils.SSHClient, error)

// SSHBuilder is the registered BuildProvider for name "ssh".
type SSHBuilder struct {
	// dial, when non-nil, overrides infra.ConnectSSH. Zero value means
	// production path. Tests swap this for a MockSSH-returning closure.
	dial dialFunc
}

// New returns an SSHBuilder wired to the production dialer.
func New() *SSHBuilder { return &SSHBuilder{} }

// Build drives clone → login → buildx --push on the builder and returns
// req.Image unchanged on success (ssh is not a content-addressed builder).
func (b *SSHBuilder) Build(ctx context.Context, req provider.BuildRequest) (string, error) {
	if err := validateBuildRequest(req); err != nil {
		return "", err
	}

	target := req.Builders[0]
	user := target.User
	if user == "" {
		// BuilderTarget.User is populated by every IaaS provider today, but
		// the interface comment (pkg/provider/infra.go) carves out future
		// non-Ubuntu images; fall back so we don't panic on a zero value.
		user = utils.DefaultUser
	}

	dial := b.dial
	if dial == nil {
		// Production path. Anonymous wrapper adapts infra.ConnectSSH's
		// `(*infra.SSHClient, error)` return to the interface we hold.
		dial = func(ctx context.Context, addr, user string, key []byte) (utils.SSHClient, error) {
			return infra.ConnectSSH(ctx, addr, user, key)
		}
	}

	stream := streamWriter(req.Output)
	progress := func(msg string) {
		// Breadcrumbs land in Output so the operator sees "dialing X",
		// "git clone Y" — plain lines alongside the streamed docker output.
		_, _ = stream.Write([]byte(msg + "\n"))
	}

	addr := target.Host + ":22"
	progress(fmt.Sprintf("ssh build: dialing builder %s", addr))
	client, err := dial(ctx, addr, user, req.SSHKey)
	if err != nil {
		return "", fmt.Errorf("ssh build: dial %s: %w", addr, err)
	}
	defer client.Close()

	// mktemp -d gives us a race-free workspace name on the builder. `/tmp`
	// lives on the cache volume's parent (builder cloud-init mounts
	// Docker's data-root on the cache volume, not /tmp) so workspace IO
	// is cheap and ephemeral — cache volume stays untouched.
	workspace, err := mktempDir(ctx, client)
	if err != nil {
		return "", fmt.Errorf("ssh build (%s): mktemp: %w", target.Name, err)
	}
	defer func() {
		// Best-effort cleanup. A failed rm should not mask the build
		// error, and succeeding cleanup should not spam Output.
		_, _ = client.Run(ctx, "rm -rf "+shellQuote(workspace))
	}()

	progress(fmt.Sprintf("ssh build: git clone %s @ %s → %s", req.GitRemote, shortSHA(req.GitRef), target.Name))
	if err := gitCloneCommit(ctx, client, workspace, req.GitRemote, req.GitRef, stream); err != nil {
		return "", fmt.Errorf("ssh build (%s): %w", target.Name, err)
	}

	progress(fmt.Sprintf("ssh build: docker login %s on %s", req.Registry.Host, target.Name))
	if err := dockerLogin(ctx, client, req.Registry, stream); err != nil {
		return "", fmt.Errorf("ssh build (%s): %w", target.Name, err)
	}

	// req.Context is repo-relative ("." for repo-root, "services/api" for
	// a sub-directory build). The CLI normalizes to this shape before
	// calling BuildProvider.Build; local uses the same field verbatim
	// against the operator's filesystem.
	buildCtx := joinRepoPath(workspace, req.Context)
	dockerfile := resolveDockerfile(buildCtx, req.Dockerfile)

	progress(fmt.Sprintf("ssh build: docker buildx build --push -t %s (platform %s)", req.Image, req.Platform))
	if err := dockerBuildxPush(ctx, client, req.Image, req.Platform, buildCtx, dockerfile, stream); err != nil {
		return "", fmt.Errorf("ssh build (%s): %w", target.Name, err)
	}

	return req.Image, nil
}

// Close is a no-op; SSHBuilder dials per-Build and closes the client there.
func (*SSHBuilder) Close() error { return nil }

// ── helpers ──────────────────────────────────────────────────────────────

func validateBuildRequest(req provider.BuildRequest) error {
	if req.Image == "" {
		return fmt.Errorf("services.%s.build: Image is required", req.Service)
	}
	if req.Platform == "" {
		return fmt.Errorf("services.%s.build: Platform is required — an empty platform would silently build for the builder's host arch", req.Service)
	}
	if req.GitRemote == "" {
		return errors.New("ssh build: GitRemote is empty — the operator's cwd must be a git checkout with an origin remote; the CLI should have inferred it via `git remote get-url origin`")
	}
	if req.GitRef == "" {
		return errors.New("ssh build: GitRef is empty — the CLI should have pinned HEAD via `git rev-parse HEAD`")
	}
	if len(req.Builders) == 0 {
		return errors.New("ssh build: no role: builder server available — the validator should have rejected this config (R1)")
	}
	if req.Builders[0].Host == "" {
		return fmt.Errorf("ssh build: builder %q has no reachable host", req.Builders[0].Name)
	}
	if len(req.SSHKey) == 0 {
		return errors.New("ssh build: SSHKey is empty — the CLI did not forward the operator's private key")
	}
	return nil
}

// mktempDir runs `mktemp -d` on the builder and returns the resulting path.
// One round-trip so we don't have to invent a naming scheme + race-check
// ourselves. Trims trailing whitespace because shells append \n.
func mktempDir(ctx context.Context, client utils.SSHClient) (string, error) {
	out, err := client.Run(ctx, "mktemp -d /tmp/nvoi-build.XXXXXXXX")
	if err != nil {
		return "", err
	}
	ws := strings.TrimSpace(string(out))
	if ws == "" {
		return "", errors.New("mktemp returned empty path")
	}
	return ws, nil
}

// gitCloneCommit clones the repo shallow-at-any-ref then fetches + checks
// out the exact SHA. Three commands, composed with `&&` so the SSH session
// reports the first non-zero exit.
//
// Why not `git clone --branch <ref>`: that only works when <ref> is a
// branch/tag name. We pin by SHA (the CLI does `git rev-parse HEAD`) to
// guarantee the builder sees the exact tree the operator is deploying,
// even if `main` advances between invocation and clone. Shallow-cloning
// then `git fetch --depth=1 origin <sha>` lets the server upload only the
// commit we care about (server-side GIT_PROTOCOL v2 support — every
// major host has it).
func gitCloneCommit(ctx context.Context, client utils.SSHClient, workspace, remote, sha string, stream io.Writer) error {
	cmd := strings.Join([]string{
		"cd " + shellQuote(workspace),
		"git init -q",
		"git remote add origin " + shellQuote(remote),
		"git fetch --depth=1 -q origin " + shellQuote(sha),
		"git checkout -q FETCH_HEAD",
	}, " && ")
	if err := client.RunStream(ctx, cmd, stream, stream); err != nil {
		return fmt.Errorf("git clone %s @ %s: %w", remote, shortSHA(sha), err)
	}
	return nil
}

// dockerLogin pipes the password over stdin — same contract as Kamal and
// pkg/core/build.go's DockerRunner. Password never appears in argv.
//
// When Username + Password are both empty we skip login entirely. Public
// registries (e.g. docker.io/library/alpine for a hypothetical unauth'd
// pull-through) don't need auth for push; but in practice a push target
// will always require credentials. The zero-cred branch is defensive so
// an empty Registry doesn't turn into a mystery `docker login ""` failure.
func dockerLogin(ctx context.Context, client utils.SSHClient, auth provider.RegistryAuth, stream io.Writer) error {
	if auth.Username == "" && auth.Password == "" {
		return nil
	}
	cmd := fmt.Sprintf("docker login %s -u %s --password-stdin",
		shellQuote(auth.Host), shellQuote(auth.Username))
	if err := client.RunWithStdin(ctx, cmd, strings.NewReader(auth.Password), stream, stream); err != nil {
		return fmt.Errorf("docker login %s: %w", auth.Host, err)
	}
	return nil
}

// dockerBuildxPush runs `docker buildx build --push` on the builder.
// `--push` is preferred over a separate `docker push` because buildx can
// push layers in parallel with build steps on registries that support it,
// and it avoids a redundant local-store write on the builder.
func dockerBuildxPush(ctx context.Context, client utils.SSHClient, image, platform, buildCtx, dockerfile string, stream io.Writer) error {
	cmd := fmt.Sprintf(
		"docker buildx build --push --progress=plain --platform %s -t %s -f %s %s",
		shellQuote(platform),
		shellQuote(image),
		shellQuote(dockerfile),
		shellQuote(buildCtx),
	)
	if err := client.RunStream(ctx, cmd, stream, stream); err != nil {
		return fmt.Errorf("docker buildx build %s: %w", image, err)
	}
	return nil
}

// joinRepoPath resolves Context (repo-relative) against the workspace.
// An empty or "." Context means "build the repo root". Trailing slashes
// and leading `./` are normalized for tidy logs.
func joinRepoPath(workspace, ctxPath string) string {
	ctxPath = strings.TrimPrefix(ctxPath, "./")
	ctxPath = strings.Trim(ctxPath, "/")
	if ctxPath == "" || ctxPath == "." {
		return workspace
	}
	return path.Join(workspace, ctxPath)
}

// resolveDockerfile returns the absolute-on-builder Dockerfile path. When
// req.Dockerfile is empty (the common case), docker defaults to
// `<buildCtx>/Dockerfile`; we make the path explicit so the buildx
// invocation is deterministic (no dependency on docker-CLI version
// defaults) and test assertions can match it verbatim.
func resolveDockerfile(buildCtx, dockerfile string) string {
	if dockerfile == "" {
		return path.Join(buildCtx, "Dockerfile")
	}
	if strings.HasPrefix(dockerfile, "/") {
		// Caller supplied an absolute path — trust it verbatim.
		return dockerfile
	}
	// Repo-relative Dockerfile path, e.g. "docker/api.Dockerfile".
	return path.Join(buildCtx, dockerfile)
}

// shellQuote wraps s in single quotes and escapes embedded single quotes
// via the close-reopen trick. Used for every argv[] that might contain
// path characters, URLs, or user-supplied platform strings.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shortSHA is a 7-char prefix for log lines. We never parse the shortened
// string — the full SHA is what lands in `git fetch`.
func shortSHA(ref string) string {
	if len(ref) >= 7 {
		return ref[:7]
	}
	return ref
}

// streamWriter returns a non-nil io.Writer for req.Output — falling back
// to io.Discard when the orchestrator passed nil (e.g. inside a test that
// doesn't care about build output).
func streamWriter(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}

func init() {
	provider.RegisterBuild(
		"ssh",
		// No credentials: the operator's SSH private key + builder
		// addresses ride on BuildRequest (populated by reconcile from
		// the already-resolved Cluster.SSHKey and InfraProvider.BuilderTargets).
		provider.CredentialSchema{Name: "ssh"},
		provider.BuildCapability{
			// ssh needs at least one role: builder server (validator R1).
			RequiresBuilders: true,
		},
		func(_ map[string]string) provider.BuildProvider { return New() },
	)
}
