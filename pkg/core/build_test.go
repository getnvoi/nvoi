package core

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/getnvoi/nvoi/internal/testutil"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// fakeBuildRunner captures every call for assertions. Password payloads
// are stored so we can verify --password-stdin never leaks creds to argv.
type fakeBuildRunner struct {
	mu       sync.Mutex
	calls    []fakeBuildCall
	loginErr error
	buildErr error
	pushErr  error
}

type fakeBuildCall struct {
	Op         string // "login" | "build" | "push"
	Host       string // for login
	Username   string // for login
	Password   string // for login (captured via stdin, not argv)
	Image      string // for build/push
	Context    string // for build
	Dockerfile string // for build
}

func (f *fakeBuildRunner) PreflightBuildx(_ context.Context) error { return nil }

func (f *fakeBuildRunner) Login(_ context.Context, host, username, password string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeBuildCall{Op: "login", Host: host, Username: username, Password: password})
	return f.loginErr
}

func (f *fakeBuildRunner) Build(_ context.Context, image, buildCtx, dockerfile, _ string, _, _ io.Writer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeBuildCall{Op: "build", Image: image, Context: buildCtx, Dockerfile: dockerfile})
	return f.buildErr
}

func (f *fakeBuildRunner) Push(_ context.Context, image string, _, _ io.Writer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeBuildCall{Op: "push", Image: image})
	return f.pushErr
}

func (f *fakeBuildRunner) ops() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	ops := make([]string, len(f.calls))
	for i, c := range f.calls {
		ops[i] = c.Op
	}
	return ops
}

// testCluster returns a Cluster wired with MockOutput so tests can inspect
// the emitted log without a real SSH/kube connection. Build never touches
// the cluster — SSH/kube are intentionally absent.
func testBuildCluster(t *testing.T) Cluster {
	t.Helper()
	sshKey, _, err := utils.GenerateEd25519Key()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return Cluster{
		AppName: "myapp", Env: "prod",
		SSHKey: sshKey,
		Output: &testutil.MockOutput{},
	}
}

// stubBuildContext creates a temp directory containing a minimal
// Dockerfile so BuildService's os.Stat pre-check passes. Returns the
// directory path for use as Context.
func stubBuildContext(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM alpine:3.19\n"), 0644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	return dir
}

// INVARIANT: BuildService runs login → build → push in that exact order.
// Any other ordering (push before build, build before login) would
// either fail at runtime or push a stale image.
func TestBuildService_LoginBuildPushOrdering(t *testing.T) {
	runner := &fakeBuildRunner{}
	ctx := stubBuildContext(t)

	err := BuildService(context.Background(), BuildServiceRequest{
		Cluster:  testBuildCluster(t),
		Name:     "api",
		Image:    "ghcr.io/org/api:v1",
		Context:  ctx,
		Host:     "ghcr.io",
		Username: "alice",
		Password: "ghp_xyz",
		Runner:   runner,
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	got := runner.ops()
	want := []string{"login", "build", "push"}
	if len(got) != len(want) {
		t.Fatalf("ops = %v, want %v", got, want)
	}
	for i, op := range want {
		if got[i] != op {
			t.Errorf("ops[%d] = %q, want %q", i, got[i], op)
		}
	}
}

// REGRESSION INVARIANT: the password is passed via the Login method's
// password parameter (which the real DockerRunner pipes through stdin)
// — NEVER mixed into argv. Leaking creds into ps/shell history is a
// real security bug in other deploy tools; locking this at the Go
// interface guarantees we can't regress.
func TestBuildService_PasswordFlowsThroughParameter(t *testing.T) {
	runner := &fakeBuildRunner{}

	err := BuildService(context.Background(), BuildServiceRequest{
		Cluster:  testBuildCluster(t),
		Name:     "api",
		Image:    "ghcr.io/org/api:v1",
		Context:  stubBuildContext(t),
		Host:     "ghcr.io",
		Username: "alice",
		Password: "super-secret-token",
		Runner:   runner,
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var loginCall *fakeBuildCall
	for i := range runner.calls {
		if runner.calls[i].Op == "login" {
			loginCall = &runner.calls[i]
			break
		}
	}
	if loginCall == nil {
		t.Fatal("login call missing")
	}
	if loginCall.Password != "super-secret-token" {
		t.Errorf("password = %q, want super-secret-token", loginCall.Password)
	}
	if loginCall.Host != "ghcr.io" || loginCall.Username != "alice" {
		t.Errorf("login host/user = %q/%q", loginCall.Host, loginCall.Username)
	}
}

// INVARIANT: default dockerfile path is <Context>/Dockerfile when none
// is specified.
func TestBuildService_DefaultDockerfilePath(t *testing.T) {
	runner := &fakeBuildRunner{}
	ctx := stubBuildContext(t)

	err := BuildService(context.Background(), BuildServiceRequest{
		Cluster: testBuildCluster(t), Name: "api", Image: "ghcr.io/org/api:v1",
		Context: ctx, Host: "ghcr.io", Username: "u", Password: "p",
		Runner: runner,
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var buildCall *fakeBuildCall
	for i := range runner.calls {
		if runner.calls[i].Op == "build" {
			buildCall = &runner.calls[i]
			break
		}
	}
	want := filepath.Join(ctx, "Dockerfile")
	if buildCall.Dockerfile != want {
		t.Errorf("dockerfile = %q, want %q", buildCall.Dockerfile, want)
	}
}

// INVARIANT: missing Dockerfile → hard error before any docker child is
// spawned. A missing build context is a deploy-time configuration bug
// (user typo'd the path), must surface immediately.
func TestBuildService_MissingDockerfile_FailsFast(t *testing.T) {
	runner := &fakeBuildRunner{}
	ctx := t.TempDir() // empty — no Dockerfile

	err := BuildService(context.Background(), BuildServiceRequest{
		Cluster: testBuildCluster(t), Name: "api", Image: "ghcr.io/org/api:v1",
		Context: ctx, Host: "ghcr.io", Username: "u", Password: "p",
		Runner: runner,
	})
	if err == nil {
		t.Fatal("expected error when Dockerfile is missing, got nil")
	}
	if len(runner.calls) != 0 {
		t.Errorf("runner must not be called when dockerfile check fails, got %v", runner.ops())
	}
}

// INVARIANT: a build failure short-circuits push. Pushing a stale image
// (from a prior successful build) would silently deploy yesterday's code.
func TestBuildService_BuildFailure_ShortCircuitsPush(t *testing.T) {
	runner := &fakeBuildRunner{buildErr: errors.New("layer corrupt")}

	err := BuildService(context.Background(), BuildServiceRequest{
		Cluster: testBuildCluster(t), Name: "api", Image: "ghcr.io/org/api:v1",
		Context: stubBuildContext(t), Host: "ghcr.io", Username: "u", Password: "p",
		Runner: runner,
	})
	if err == nil {
		t.Fatal("expected error from failing build")
	}
	ops := runner.ops()
	for _, op := range ops {
		if op == "push" {
			t.Errorf("push called after build failure — would deploy stale image. ops: %v", ops)
		}
	}
}

// INVARIANT: login failure short-circuits build and push.
func TestBuildService_LoginFailure_ShortCircuits(t *testing.T) {
	runner := &fakeBuildRunner{loginErr: errors.New("auth denied")}

	err := BuildService(context.Background(), BuildServiceRequest{
		Cluster: testBuildCluster(t), Name: "api", Image: "ghcr.io/org/api:v1",
		Context: stubBuildContext(t), Host: "ghcr.io", Username: "u", Password: "p",
		Runner: runner,
	})
	if err == nil {
		t.Fatal("expected login error to propagate")
	}
	for _, c := range runner.calls {
		if c.Op == "build" || c.Op == "push" {
			t.Errorf("login failure must short-circuit %s — auth denied means we can't pull base images either", c.Op)
		}
	}
}
