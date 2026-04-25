package daytona

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getnvoi/nvoi/pkg/provider"
)

// init drops the production 2-second poll intervals (docker readiness +
// long-running session polling) to millisecond scale so the package fits
// the 2s-per-package test budget. Non-negotiable — without this,
// TestBuild_HappyPath burns the entire budget waiting on the first ticker
// tick. Mirrors kube.SetTestTiming() convention.
func init() {
	dockerPollInterval = time.Millisecond
	sessionPollInterval = time.Millisecond
}

// TestRegistersOnImport verifies init() wires "daytona" into the build
// registry with the capability bits the validator depends on. Matches the
// ssh + local tests — capability is static data consumed by
// ValidateConfig before any credentials resolve.
func TestRegistersOnImport(t *testing.T) {
	schema, err := provider.GetBuildSchema("daytona")
	if err != nil {
		t.Fatalf("daytona build provider not registered: %v", err)
	}
	if schema.Name != "daytona" {
		t.Errorf("schema.Name: got %q, want daytona", schema.Name)
	}
	// The api_key field is the single credential, wired to DAYTONA_API_KEY.
	// 9b39daf parity gate — regressing this would silently widen the env
	// surface and break operators who already have the env var set.
	if len(schema.Fields) != 1 {
		t.Fatalf("schema.Fields: got %d, want 1", len(schema.Fields))
	}
	f := schema.Fields[0]
	if f.Key != "api_key" || f.EnvVar != "DAYTONA_API_KEY" || !f.Required {
		t.Errorf("api_key field mismatch: %+v", f)
	}

	caps, err := provider.GetBuildCapability("daytona")
	if err != nil {
		t.Fatalf("GetBuildCapability(daytona): %v", err)
	}
	if caps.RequiresBuilders {
		t.Errorf("daytona.RequiresBuilders: got true, want false (daytona runs inside a managed sandbox, not on role:builder servers)")
	}
}

// TestResolveRequiresAPIKey asserts the cred schema enforces the required
// api_key at Resolve time — otherwise a misconfigured .env would surface
// the error deep inside Build instead of at ValidateCredentials.
func TestResolveRequiresAPIKey(t *testing.T) {
	if _, err := provider.ResolveBuild("daytona", map[string]string{}); err == nil {
		t.Fatal("ResolveBuild(daytona, {}) must fail — api_key is required")
	}
	b, err := provider.ResolveBuild("daytona", map[string]string{"api_key": "test-key"})
	if err != nil {
		t.Fatalf("ResolveBuild(daytona, {api_key}): %v", err)
	}
	if b == nil {
		t.Fatal("ResolveBuild returned nil")
	}
	if err := b.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// ── fake client + sandbox ────────────────────────────────────────────────

// fakeSandbox records every call a Build() makes on the sandbox. Keeps the
// surface narrow — only the methods exercised by Build.
type fakeSandbox struct {
	mu       sync.Mutex
	uploads  []fakeUpload
	clones   []fakeClone
	execs    []fakeExec
	sessions []string
	started  bool
	stopped  bool
	state    string

	// dockerReadyAfter controls how many `docker info` polls waitForDocker
	// needs before succeeding. 0 = first poll succeeds.
	dockerReadyAfter int
	dockerPolls      int

	// sessionCmds maps sessionID+cmdID to (exitCode, finished) — used by
	// the split-session-exec dance for long-running buildx.
	sessionCmds map[string]fakeSessionCmd

	// execErr injects an error on a specific command prefix.
	execErr map[string]error

	// cloneErr injects an error on Clone.
	cloneErr error
}

type fakeUpload struct {
	dest string
	data []byte
}

type fakeClone struct {
	url, path, branch, user, pass string
}

type fakeExec struct {
	cmd     string
	timeout time.Duration
}

type fakeSessionCmd struct {
	exitCode int
	finished bool
}

func newFakeSandbox() *fakeSandbox {
	return &fakeSandbox{
		state:       "STARTED",
		sessionCmds: map[string]fakeSessionCmd{},
		execErr:     map[string]error{},
	}
}

func (f *fakeSandbox) Upload(_ context.Context, data []byte, dest string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploads = append(f.uploads, fakeUpload{dest: dest, data: data})
	return nil
}

func (f *fakeSandbox) Clone(_ context.Context, url, path, branch, user, pass string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cloneErr != nil {
		return f.cloneErr
	}
	f.clones = append(f.clones, fakeClone{url, path, branch, user, pass})
	return nil
}

func (f *fakeSandbox) Exec(_ context.Context, command string, timeout time.Duration) (string, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.execs = append(f.execs, fakeExec{cmd: command, timeout: timeout})

	// Targeted error injection — prefix match.
	for prefix, err := range f.execErr {
		if strings.HasPrefix(command, prefix) {
			return "", 1, err
		}
	}

	// docker info poll — succeed after dockerReadyAfter polls.
	if strings.HasPrefix(command, "docker info") {
		f.dockerPolls++
		if f.dockerPolls > f.dockerReadyAfter {
			return "ready\n", 0, nil
		}
		return "", 1, nil
	}
	return "", 0, nil
}

func (f *fakeSandbox) CreateSession(_ context.Context, sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions = append(f.sessions, sessionID)
	return nil
}

func (f *fakeSandbox) ExecSessionAsync(_ context.Context, sessionID, command string) (string, error) {
	cmdID := sessionID + "-cmd"
	f.mu.Lock()
	defer f.mu.Unlock()
	f.execs = append(f.execs, fakeExec{cmd: command})
	// Default: buildx exits 0 on first SessionCommand poll.
	f.sessionCmds[sessionID+":"+cmdID] = fakeSessionCmd{exitCode: 0, finished: true}
	return cmdID, nil
}

func (f *fakeSandbox) StreamSessionLogs(_ context.Context, _, _ string, stdout, stderr chan<- string) error {
	// Close immediately — Build's consumer drains until both channels shut.
	close(stdout)
	close(stderr)
	return nil
}

func (f *fakeSandbox) SessionCommand(_ context.Context, sessionID, commandID string) (int, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	res, ok := f.sessionCmds[sessionID+":"+commandID]
	if !ok {
		return 0, false, nil
	}
	return res.exitCode, res.finished, nil
}

func (f *fakeSandbox) DeleteSession(_ context.Context, _ string) error { return nil }

func (f *fakeSandbox) Start(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = true
	f.state = "STARTED"
	return nil
}

func (f *fakeSandbox) Stop(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = true
	return nil
}

func (f *fakeSandbox) State() string { return f.state }

// fakeClient returns a single pre-seeded sandbox.
type fakeClient struct {
	sb          *fakeSandbox
	findCalls   int
	ensureCalls int
	findErr     error
	ensureErr   error
}

func (c *fakeClient) FindOrStartOrCreate(_ context.Context, _ string) (sandbox, error) {
	c.findCalls++
	if c.findErr != nil {
		return nil, c.findErr
	}
	return c.sb, nil
}

func (c *fakeClient) EnsureSnapshot(_ context.Context) error {
	c.ensureCalls++
	return c.ensureErr
}

// ── Build happy path + validation tests ──────────────────────────────────

// defaultRequest mirrors ssh's defaultRequest. Keeps tests readable.
func defaultRequest(output io.Writer) provider.BuildRequest {
	return provider.BuildRequest{
		Service:    "api",
		Context:    "services/api",
		Dockerfile: "",
		Platform:   "linux/arm64",
		Image:      "ghcr.io/org/api:v1-20260421-000000",
		Registry:   provider.RegistryAuth{Host: "ghcr.io", Username: "org", Password: "ghp_secret"},
		GitRemote:  "https://github.com/org/repo.git",
		GitRef:     "cafef00d1234567890abcdef1234567890abcdef",
		Output:     output,
	}
}

// TestBuild_HappyPath asserts the full flow:
//   - EnsureSnapshot + FindOrStartOrCreate are called (session setup).
//   - waitForDocker polls and resolves.
//   - Clone fires with the remote + repo path + empty branch (SHA pin via
//     a follow-up git fetch).
//   - SHA is fetched + checked out inside the sandbox.
//   - docker login pipes the password via --password-stdin in a shell
//     pipeline, never argv.
//   - docker buildx build --push runs through the split-session-exec
//     pathway with the right flags.
//   - Stop is called on return (sandbox preserved for cache reuse).
//   - Return value is req.Image (not a rewrite — ssh contract parity).
func TestBuild_HappyPath(t *testing.T) {
	sb := newFakeSandbox()
	c := &fakeClient{sb: sb}
	b := &DaytonaBuilder{
		apiKey:    "test-key",
		newClient: func(string) (client, error) { return c, nil },
	}

	var out bytes.Buffer
	ref, err := b.Build(context.Background(), defaultRequest(&out))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if ref != "ghcr.io/org/api:v1-20260421-000000" {
		t.Errorf("ref: got %q, want req.Image verbatim", ref)
	}

	// Sandbox acquisition fired exactly once.
	if c.findCalls != 1 {
		t.Errorf("FindOrStartOrCreate calls: got %d, want 1", c.findCalls)
	}

	// Clone at HEAD (branch ""), followed by SHA fetch + checkout.
	if len(sb.clones) != 1 {
		t.Fatalf("clones: got %d, want 1: %v", len(sb.clones), sb.clones)
	}
	if sb.clones[0].url != "https://github.com/org/repo.git" {
		t.Errorf("clone url: got %q", sb.clones[0].url)
	}
	if sb.clones[0].path != sandboxHome+"/src" {
		t.Errorf("clone path: got %q, want %s/src", sb.clones[0].path, sandboxHome)
	}
	if sb.clones[0].branch != "" {
		t.Errorf("clone branch: got %q, want empty (SHA pinning via follow-up fetch)", sb.clones[0].branch)
	}

	// Follow-up SHA fetch + checkout.
	foundFetch := false
	for _, e := range sb.execs {
		if strings.Contains(e.cmd, "git fetch --depth=1 origin 'cafef00d1234567890abcdef1234567890abcdef'") &&
			strings.Contains(e.cmd, "git checkout FETCH_HEAD") {
			foundFetch = true
			break
		}
	}
	if !foundFetch {
		t.Errorf("expected `git fetch --depth=1 origin <sha> && git checkout FETCH_HEAD`; execs: %v", sb.execs)
	}

	// docker login: password via printf stdin pipe, --password-stdin flag.
	var loginCmd string
	for _, e := range sb.execs {
		if strings.HasPrefix(e.cmd, "printf") && strings.Contains(e.cmd, "docker login") {
			loginCmd = e.cmd
			break
		}
	}
	if loginCmd == "" {
		t.Fatalf("no docker login via stdin found; execs: %v", sb.execs)
	}
	if !strings.Contains(loginCmd, "--password-stdin") {
		t.Errorf("docker login missing --password-stdin: %q", loginCmd)
	}
	if !strings.Contains(loginCmd, "'ghcr.io'") || !strings.Contains(loginCmd, "-u 'org'") {
		t.Errorf("docker login args: %q", loginCmd)
	}

	// docker buildx — via async session, not direct Exec. Assert the cmd
	// string went through ExecSessionAsync (captured alongside Exec in
	// execs for simplicity; the prefix shape is the tell).
	var buildCmd string
	for _, e := range sb.execs {
		if strings.HasPrefix(e.cmd, "docker buildx build") {
			buildCmd = e.cmd
			break
		}
	}
	if buildCmd == "" {
		t.Fatalf("no docker buildx build call; execs: %v", sb.execs)
	}
	for _, want := range []string{
		"--output type=image,push=true",
		"--platform 'linux/arm64'",
		"--tag 'ghcr.io/org/api:v1-20260421-000000'",
		"--file '" + sandboxHome + "/src/services/api/Dockerfile'",
		"'" + sandboxHome + "/src/services/api'",
	} {
		if !strings.Contains(buildCmd, want) {
			t.Errorf("docker buildx missing %q; full: %q", want, buildCmd)
		}
	}

	// A session was created (split-session-exec dance).
	if len(sb.sessions) == 0 {
		t.Error("expected at least one CreateSession call for the long-running buildx")
	}

	// Stop called on return.
	if !sb.stopped {
		t.Error("sandbox must be Stopped on Build return (preserve cache)")
	}
}

// TestBuild_ValidationErrors covers every zero-value guard on BuildRequest.
// Validator R1 catches most of these at config time, but Build() must
// self-guard so a regressed CLI fails loudly.
func TestBuild_ValidationErrors(t *testing.T) {
	b := New("test-key")

	cases := []struct {
		name   string
		mutate func(*provider.BuildRequest)
		want   string
	}{
		{"no image", func(r *provider.BuildRequest) { r.Image = "" }, "Image is required"},
		{"no platform", func(r *provider.BuildRequest) { r.Platform = "" }, "Platform is required"},
		{"no git remote", func(r *provider.BuildRequest) { r.GitRemote = "" }, "GitRemote is empty"},
		{"no git ref", func(r *provider.BuildRequest) { r.GitRef = "" }, "GitRef is empty"},
		{"no service", func(r *provider.BuildRequest) { r.Service = "" }, "Service name is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := defaultRequest(io.Discard)
			tc.mutate(&req)
			_, err := b.Build(context.Background(), req)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got err=%v, want substring %q", err, tc.want)
			}
		})
	}
}

// TestBuild_SandboxAcquireFailure_PropagatesError locks the error-wrapping
// contract — operator needs to know WHICH step failed.
func TestBuild_SandboxAcquireFailure_PropagatesError(t *testing.T) {
	acquireErr := errors.New("rate limited")
	c := &fakeClient{sb: newFakeSandbox(), findErr: acquireErr}
	b := &DaytonaBuilder{
		apiKey:    "test-key",
		newClient: func(string) (client, error) { return c, nil },
	}
	_, err := b.Build(context.Background(), defaultRequest(io.Discard))
	if err == nil || !strings.Contains(err.Error(), "sandbox acquire") {
		t.Errorf("got %v, want wrapped 'sandbox acquire' error", err)
	}
	if !errors.Is(err, acquireErr) {
		t.Errorf("error should wrap acquire error; got %v", err)
	}
}

// TestBuild_EmptyRegistryCreds_SkipsLogin parity with ssh — empty creds
// means skip `docker login` entirely, not fire it with empty args.
func TestBuild_EmptyRegistryCreds_SkipsLogin(t *testing.T) {
	sb := newFakeSandbox()
	c := &fakeClient{sb: sb}
	b := &DaytonaBuilder{
		apiKey:    "test-key",
		newClient: func(string) (client, error) { return c, nil },
	}

	req := defaultRequest(io.Discard)
	req.Registry = provider.RegistryAuth{Host: "ghcr.io"}
	if _, err := b.Build(context.Background(), req); err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, e := range sb.execs {
		if strings.Contains(e.cmd, "docker login") {
			t.Errorf("docker login must be skipped on empty creds; got %q", e.cmd)
		}
	}
}

// TestSanitize locks the service-name → sandbox-name rules. 9b39daf parity:
// '/' and ':' replaced, capped at 50 chars.
func TestSanitize(t *testing.T) {
	cases := []struct{ in, want string }{
		{"api", "api"},
		{"org/api", "org-api"},
		{"org/api:v1", "org-api-v1"},
		{strings.Repeat("a", 60), strings.Repeat("a", 50)},
	}
	for _, c := range cases {
		if got := sanitize(c.in); got != c.want {
			t.Errorf("sanitize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestJoinRepoPath locks the repo-relative Context resolution — parity with
// ssh's joinRepoPath, since both remote builders share the "Context is
// repo-relative" contract.
func TestJoinRepoPath(t *testing.T) {
	cases := []struct {
		repo, ctx, want string
	}{
		{"/home/daytona/src", "", "/home/daytona/src"},
		{"/home/daytona/src", ".", "/home/daytona/src"},
		{"/home/daytona/src", "services/api", "/home/daytona/src/services/api"},
		{"/home/daytona/src", "./services/api", "/home/daytona/src/services/api"},
		{"/home/daytona/src", "services/api/", "/home/daytona/src/services/api"},
	}
	for _, c := range cases {
		if got := joinRepoPath(c.repo, c.ctx); got != c.want {
			t.Errorf("joinRepoPath(%q, %q) = %q, want %q", c.repo, c.ctx, got, c.want)
		}
	}
}
