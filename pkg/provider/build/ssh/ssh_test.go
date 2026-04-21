package ssh

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/internal/testutil"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// TestRegistersOnImport verifies init() wires "ssh" into the build registry
// with the capability bits the validator depends on. R1 in
// internal/reconcile/validate.go and the provider-level gates in
// pkg/provider/build.go both read these at ValidateConfig time — before any
// credentials are resolved — so they MUST live as static data on the
// registered entry, not behind a live BuildProvider instance.
func TestRegistersOnImport(t *testing.T) {
	if _, err := provider.GetBuildSchema("ssh"); err != nil {
		t.Fatalf("ssh build provider not registered: %v", err)
	}
	caps, err := provider.GetBuildCapability("ssh")
	if err != nil {
		t.Fatalf("GetBuildCapability(ssh): %v", err)
	}
	if !caps.RequiresBuilders {
		t.Errorf("ssh.RequiresBuilders: got false, want true (ssh is a remote-substrate builder)")
	}
}

// TestResolveReturnsInstance verifies ResolveBuild round-trips through the
// factory with an empty cred map (ssh has no credentials of its own — the
// SSH key rides on BuildRequest, not on the provider cred bag).
func TestResolveReturnsInstance(t *testing.T) {
	b, err := provider.ResolveBuild("ssh", nil)
	if err != nil {
		t.Fatalf("ResolveBuild(ssh): %v", err)
	}
	if b == nil {
		t.Fatal("ResolveBuild(ssh) returned nil provider")
	}
	if err := b.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// buildFixture wires an SSHBuilder to a MockSSH through the dial seam and
// pre-seeds the mktemp response. Every happy-path test below reuses this
// plumbing so assertions focus on command shape, not setup.
type buildFixture struct {
	builder *SSHBuilder
	ssh     *testutil.MockSSH
	dialArg struct {
		addr string
		user string
		key  []byte
	}
	workspace string
}

// newBuildFixture returns a fixture whose MockSSH answers every command
// the happy path requires — mktemp (to return a fixed workspace), the
// composed `cd … && git init … && git fetch … && git checkout …` string,
// `docker login`, `docker buildx build`, and the cleanup `rm -rf`.
// Tests inspect fx.ssh.Calls, fx.ssh.Stdins, fx.ssh.Uploads for shape.
func newBuildFixture(t *testing.T) *buildFixture {
	t.Helper()
	const workspace = "/tmp/nvoi-build.ABCDEFGH"

	mock := testutil.NewMockSSH(map[string]testutil.MockResult{
		"mktemp -d /tmp/nvoi-build.XXXXXXXX": {Output: []byte(workspace + "\n")},
	})
	// Prefix matchers in declaration order — first match wins per
	// MockSSH.Run. Anchor each on a unique leading token so a `git
	// fetch` call can't accidentally match `docker buildx build`.
	mock.Prefixes = []testutil.MockPrefix{
		// The clone pipeline is composed with `&&`, so the first word is
		// `cd <workspace>`. Match on that.
		{Prefix: "cd '" + workspace + "'", Result: testutil.MockResult{Output: []byte("cloned\n")}},
		{Prefix: "docker login", Result: testutil.MockResult{Output: []byte("Login Succeeded\n")}},
		{Prefix: "docker buildx build", Result: testutil.MockResult{Output: []byte("#1 [internal] load build definition\n")}},
		{Prefix: "rm -rf '" + workspace + "'", Result: testutil.MockResult{}},
	}

	fx := &buildFixture{ssh: mock, workspace: workspace}
	fx.builder = &SSHBuilder{
		dial: func(_ context.Context, addr, user string, key []byte) (utils.SSHClient, error) {
			fx.dialArg.addr = addr
			fx.dialArg.user = user
			fx.dialArg.key = key
			return mock, nil
		},
	}
	return fx
}

// defaultRequest builds a BuildRequest with all the fields validateBuildRequest
// requires. Individual tests override specific fields to exercise error paths.
func defaultRequest(output io.Writer) provider.BuildRequest {
	return provider.BuildRequest{
		Service:    "api",
		Context:    "services/api",
		Dockerfile: "", // default to <context>/Dockerfile
		Platform:   "linux/arm64",
		Image:      "ghcr.io/org/api:v1-20260421-000000",
		Registry:   provider.RegistryAuth{Host: "ghcr.io", Username: "org", Password: "ghp_secret"},
		Builders: []provider.BuilderTarget{
			{Name: "nvoi-demo-prod-builder-1", Host: "203.0.113.10", User: "deploy"},
		},
		SSHKey:    []byte("private-key-bytes"),
		GitRemote: "https://github.com/org/repo.git",
		GitRef:    "cafef00d1234567890abcdef1234567890abcdef",
		Output:    output,
	}
}

// TestBuild_HappyPath_CommandShape is the core contract test. Asserts:
//   - Dials Builders[0] at Host:22 as User with SSHKey flowing verbatim.
//   - mktemp fires first (one round-trip, no racy path invention).
//   - git pipeline: `cd <ws> && git init -q && git remote add origin <url> &&
//     git fetch --depth=1 -q origin <sha> && git checkout -q FETCH_HEAD`.
//     Pinning by SHA (not branch) is load-bearing — the CLI passes
//     `git rev-parse HEAD`, and the builder's tree must match exactly.
//   - docker login: `-u <user> --password-stdin`, password on stdin, never argv.
//   - docker buildx build: `--push --platform <p> -t <image> -f <dockerfile> <ctx>`.
//   - Cleanup `rm -rf <ws>` fires on success (Defer + best-effort).
//   - Returns req.Image unchanged (ssh is not content-addressed).
func TestBuild_HappyPath_CommandShape(t *testing.T) {
	fx := newBuildFixture(t)
	var out bytes.Buffer
	req := defaultRequest(&out)

	ref, err := fx.builder.Build(context.Background(), req)
	if err != nil {
		t.Fatalf("Build: unexpected error %v", err)
	}
	if ref != req.Image {
		t.Errorf("ref: got %q, want req.Image %q (ssh must not rewrite)", ref, req.Image)
	}

	// Dial target + auth.
	if fx.dialArg.addr != "203.0.113.10:22" {
		t.Errorf("dial addr: got %q, want 203.0.113.10:22", fx.dialArg.addr)
	}
	if fx.dialArg.user != "deploy" {
		t.Errorf("dial user: got %q, want deploy", fx.dialArg.user)
	}
	if string(fx.dialArg.key) != "private-key-bytes" {
		t.Errorf("dial key: got %q, want private-key-bytes", fx.dialArg.key)
	}

	// Calls ordered: mktemp → clone pipeline → docker login → docker buildx → rm.
	// MockSSH records every Run/RunStream/RunWithStdin into the same Calls slice.
	calls := fx.ssh.Calls
	if len(calls) < 5 {
		t.Fatalf("expected at least 5 SSH calls (mktemp, clone, login, build, cleanup); got %d: %v", len(calls), calls)
	}
	wantFirst := "mktemp -d /tmp/nvoi-build.XXXXXXXX"
	if calls[0] != wantFirst {
		t.Errorf("call[0]: got %q, want %q", calls[0], wantFirst)
	}

	// Clone pipeline: one composed command with `&&` joiners, pinning the
	// exact SHA via `git fetch --depth=1 origin <sha>`.
	var cloneCmd string
	for _, c := range calls {
		if strings.HasPrefix(c, "cd '"+fx.workspace+"'") {
			cloneCmd = c
			break
		}
	}
	if cloneCmd == "" {
		t.Fatalf("no clone pipeline found in calls: %v", calls)
	}
	for _, want := range []string{
		"git init -q",
		"git remote add origin 'https://github.com/org/repo.git'",
		"git fetch --depth=1 -q origin 'cafef00d1234567890abcdef1234567890abcdef'",
		"git checkout -q FETCH_HEAD",
	} {
		if !strings.Contains(cloneCmd, want) {
			t.Errorf("clone pipeline missing %q; full: %q", want, cloneCmd)
		}
	}

	// docker login: password via stdin, --password-stdin flag present.
	var loginCmd string
	for c := range fx.ssh.Stdins {
		if strings.HasPrefix(c, "docker login") {
			loginCmd = c
			break
		}
	}
	if loginCmd == "" {
		t.Fatalf("no docker login call found in Stdins: %v", fx.ssh.Stdins)
	}
	if !strings.Contains(loginCmd, "--password-stdin") {
		t.Errorf("docker login missing --password-stdin: %q", loginCmd)
	}
	if !strings.Contains(loginCmd, "'ghcr.io'") || !strings.Contains(loginCmd, "-u 'org'") {
		t.Errorf("docker login args unexpected: %q", loginCmd)
	}
	if got := string(fx.ssh.Stdins[loginCmd]); got != "ghp_secret" {
		t.Errorf("password stdin: got %q, want %q", got, "ghp_secret")
	}

	// docker buildx: `--push --platform X -t image -f dockerfile <ctx>`.
	var buildCmd string
	for _, c := range calls {
		if strings.HasPrefix(c, "docker buildx build") {
			buildCmd = c
			break
		}
	}
	if buildCmd == "" {
		t.Fatalf("no docker buildx build call found: %v", calls)
	}
	for _, want := range []string{
		"--push",
		"--platform 'linux/arm64'",
		"-t 'ghcr.io/org/api:v1-20260421-000000'",
		"-f '" + fx.workspace + "/services/api/Dockerfile'",
		"'" + fx.workspace + "/services/api'",
	} {
		if !strings.Contains(buildCmd, want) {
			t.Errorf("docker buildx missing %q; full: %q", want, buildCmd)
		}
	}

	// Cleanup. Must run even after success.
	foundCleanup := false
	for _, c := range calls {
		if strings.HasPrefix(c, "rm -rf '"+fx.workspace+"'") {
			foundCleanup = true
			break
		}
	}
	if !foundCleanup {
		t.Error("expected `rm -rf <workspace>` cleanup call after successful build")
	}

	// Output stream: the docker buildx stream lands in req.Output so the
	// operator sees the build progress in real time.
	if out.Len() == 0 {
		t.Error("expected docker stdout to land in req.Output")
	}
}

// TestBuild_RootContext asserts that an empty or "." Context means
// "build the repo root" — no trailing `/./` junk in the buildx argv.
func TestBuild_RootContext(t *testing.T) {
	fx := newBuildFixture(t)
	req := defaultRequest(io.Discard)
	req.Context = ""

	if _, err := fx.builder.Build(context.Background(), req); err != nil {
		t.Fatalf("Build: %v", err)
	}

	var buildCmd string
	for _, c := range fx.ssh.Calls {
		if strings.HasPrefix(c, "docker buildx build") {
			buildCmd = c
			break
		}
	}
	// Dockerfile defaults to <workspace>/Dockerfile, build context is <workspace>.
	if !strings.Contains(buildCmd, "-f '"+fx.workspace+"/Dockerfile'") {
		t.Errorf("empty Context should make Dockerfile sit at workspace root; got %q", buildCmd)
	}
	if !strings.HasSuffix(buildCmd, " '"+fx.workspace+"'") {
		t.Errorf("empty Context should make buildx target <workspace>; got %q", buildCmd)
	}
}

// TestBuild_CustomDockerfilePath asserts req.Dockerfile (repo-relative)
// resolves under the workspace, not from CWD on the builder.
func TestBuild_CustomDockerfilePath(t *testing.T) {
	fx := newBuildFixture(t)
	req := defaultRequest(io.Discard)
	req.Dockerfile = "docker/api.Dockerfile"

	if _, err := fx.builder.Build(context.Background(), req); err != nil {
		t.Fatalf("Build: %v", err)
	}
	var buildCmd string
	for _, c := range fx.ssh.Calls {
		if strings.HasPrefix(c, "docker buildx build") {
			buildCmd = c
			break
		}
	}
	wantDockerfile := "-f '" + fx.workspace + "/services/api/docker/api.Dockerfile'"
	if !strings.Contains(buildCmd, wantDockerfile) {
		t.Errorf("custom Dockerfile should resolve under workspace+Context; got %q", buildCmd)
	}
}

// TestBuild_AbsoluteDockerfilePath honors an absolute path verbatim.
// Rare in practice, but the contract: `/` prefix = trust the caller.
func TestBuild_AbsoluteDockerfilePath(t *testing.T) {
	fx := newBuildFixture(t)
	req := defaultRequest(io.Discard)
	req.Dockerfile = "/abs/Dockerfile"

	if _, err := fx.builder.Build(context.Background(), req); err != nil {
		t.Fatalf("Build: %v", err)
	}
	var buildCmd string
	for _, c := range fx.ssh.Calls {
		if strings.HasPrefix(c, "docker buildx build") {
			buildCmd = c
			break
		}
	}
	if !strings.Contains(buildCmd, "-f '/abs/Dockerfile'") {
		t.Errorf("absolute Dockerfile should pass through verbatim; got %q", buildCmd)
	}
}

// TestBuild_EmptyRegistryCreds_SkipsLogin asserts no `docker login` when
// Username + Password are both empty. Guards against a mystery
// "docker login <empty>" failure when a caller forgets to populate Registry.
func TestBuild_EmptyRegistryCreds_SkipsLogin(t *testing.T) {
	fx := newBuildFixture(t)
	req := defaultRequest(io.Discard)
	req.Registry = provider.RegistryAuth{Host: "ghcr.io"}

	if _, err := fx.builder.Build(context.Background(), req); err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, c := range fx.ssh.Calls {
		if strings.HasPrefix(c, "docker login") {
			t.Errorf("docker login should be skipped on empty creds; got call %q", c)
		}
	}
}

// TestBuild_DefaultsUserWhenEmpty locks the fallback to utils.DefaultUser
// when BuilderTarget.User is empty. All IaaS providers fill it today, but
// BuilderTarget.User's comment carves out future non-Ubuntu images, so the
// provider must not panic on a zero value.
func TestBuild_DefaultsUserWhenEmpty(t *testing.T) {
	fx := newBuildFixture(t)
	req := defaultRequest(io.Discard)
	req.Builders[0].User = ""

	if _, err := fx.builder.Build(context.Background(), req); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if fx.dialArg.user != utils.DefaultUser {
		t.Errorf("dial user: got %q, want fallback %q", fx.dialArg.user, utils.DefaultUser)
	}
}

// TestBuild_CleanupRunsOnFailure proves the `rm -rf` defer runs even when
// the build step fails — otherwise failed builds accumulate workspaces on
// the builder and eat disk over time.
func TestBuild_CleanupRunsOnFailure(t *testing.T) {
	fx := newBuildFixture(t)
	// Re-declare prefixes with a failing buildx; same order.
	fx.ssh.Prefixes = []testutil.MockPrefix{
		{Prefix: "cd '" + fx.workspace + "'", Result: testutil.MockResult{Output: []byte("cloned\n")}},
		{Prefix: "docker login", Result: testutil.MockResult{Output: []byte("Login Succeeded\n")}},
		{Prefix: "docker buildx build", Result: testutil.MockResult{Err: errors.New("layer write failed")}},
		{Prefix: "rm -rf '" + fx.workspace + "'", Result: testutil.MockResult{}},
	}

	_, err := fx.builder.Build(context.Background(), defaultRequest(io.Discard))
	if err == nil {
		t.Fatal("expected error from failing buildx")
	}
	foundCleanup := false
	for _, c := range fx.ssh.Calls {
		if strings.HasPrefix(c, "rm -rf '"+fx.workspace+"'") {
			foundCleanup = true
			break
		}
	}
	if !foundCleanup {
		t.Error("cleanup must run even when build fails")
	}
}

// TestBuild_ValidationErrors covers every zero-value guard on BuildRequest.
// Each branch is load-bearing — the validator R1 rejects most of these
// configs before Build() is called, but the provider must self-guard so a
// regressed CLI fails loudly instead of silently building garbage.
func TestBuild_ValidationErrors(t *testing.T) {
	b := New()
	base := defaultRequest(io.Discard)

	cases := []struct {
		name    string
		mutate  func(*provider.BuildRequest)
		wantSub string
	}{
		{"no image", func(r *provider.BuildRequest) { r.Image = "" }, "Image is required"},
		{"no platform", func(r *provider.BuildRequest) { r.Platform = "" }, "Platform is required"},
		{"no git remote", func(r *provider.BuildRequest) { r.GitRemote = "" }, "GitRemote is empty"},
		{"no git ref", func(r *provider.BuildRequest) { r.GitRef = "" }, "GitRef is empty"},
		{"no builders", func(r *provider.BuildRequest) { r.Builders = nil }, "no role: builder server available"},
		{"builder no host", func(r *provider.BuildRequest) { r.Builders[0].Host = "" }, "no reachable host"},
		{"no ssh key", func(r *provider.BuildRequest) { r.SSHKey = nil }, "SSHKey is empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := base
			req.Builders = append([]provider.BuilderTarget(nil), base.Builders...) // deep-copy so mutate doesn't leak
			tc.mutate(&req)
			_, err := b.Build(context.Background(), req)
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("got err=%v, want substring %q", err, tc.wantSub)
			}
		})
	}
}

// TestBuild_DialFailure_PropagatesError locks the error-wrapping contract
// on the SSH dial — operators need to see WHICH builder failed in the
// error, for a multi-builder future.
func TestBuild_DialFailure_PropagatesError(t *testing.T) {
	dialErr := errors.New("connection refused")
	b := &SSHBuilder{
		dial: func(context.Context, string, string, []byte) (utils.SSHClient, error) {
			return nil, dialErr
		},
	}
	_, err := b.Build(context.Background(), defaultRequest(io.Discard))
	if err == nil {
		t.Fatal("expected dial error")
	}
	if !strings.Contains(err.Error(), "203.0.113.10:22") {
		t.Errorf("error should name the builder addr; got %v", err)
	}
	if !errors.Is(err, dialErr) {
		t.Errorf("error should wrap the dial error; got %v", err)
	}
}
