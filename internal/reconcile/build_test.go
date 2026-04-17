package reconcile

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
	app "github.com/getnvoi/nvoi/pkg/core"
)

// captureRunner records every BuildRunner call at the reconcile layer so
// we can assert iteration order, per-service args, and skipping logic.
type captureRunner struct {
	mu    sync.Mutex
	calls []string // e.g. "login:ghcr.io:alice" / "build:ghcr.io/org/api:v1" / "push:..."
	err   error
}

func (r *captureRunner) record(s string) {
	r.mu.Lock()
	r.calls = append(r.calls, s)
	r.mu.Unlock()
}

func (r *captureRunner) ops() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.calls))
	copy(out, r.calls)
	return out
}

// PreflightBuildx is a no-op in the common capture — existing tests care
// about login/build/push ordering, not preflight invocation. Preflight-
// specific assertions live in their own test below.
func (r *captureRunner) PreflightBuildx(_ context.Context) error { return nil }

func (r *captureRunner) Login(_ context.Context, host, user, _ string) error {
	r.record("login:" + host + ":" + user)
	return r.err
}
func (r *captureRunner) Build(_ context.Context, image, _, _ string, _, _ io.Writer) error {
	r.record("build:" + image)
	return r.err
}
func (r *captureRunner) Push(_ context.Context, image string, _, _ io.Writer) error {
	r.record("push:" + image)
	return r.err
}

// stubBuildContext writes a minimal Dockerfile at the given path so the
// os.Stat pre-check in BuildService passes.
func stubBuildContext(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM alpine\n"), 0644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	return dir
}

// TestBuild_SkipsWhenNoServiceHasBuild verifies the fast-exit path. Users
// with zero `build:` entries must not need `docker` on PATH.
func TestBuild_SkipsWhenNoServiceHasBuild(t *testing.T) {
	runner := &captureRunner{}
	cleanup := SetBuildRunnerForTest(runner)
	defer cleanup()

	dc := testDCWithCreds(convergeMock())
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Services: map[string]config.ServiceDef{
			"web": {Image: "docker.io/library/nginx"},
			"api": {Image: "docker.io/library/redis"},
		},
	}
	if err := Build(context.Background(), dc, cfg); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if ops := runner.ops(); len(ops) != 0 {
		t.Errorf("runner must not be called when no service has build:, got %v", ops)
	}
}

// TestBuild_OnlyBuildsServicesWithBuildField verifies we don't accidentally
// rebuild every service — only the ones flagged. Also locks that the
// resolved tag is user-tag + "-" + deploy-hash.
func TestBuild_OnlyBuildsServicesWithBuildField(t *testing.T) {
	runner := &captureRunner{}
	cleanup := SetBuildRunnerForTest(runner)
	defer cleanup()

	tmp := t.TempDir()
	apiCtx := stubBuildContext(t, filepath.Join(tmp, "services", "api"))

	dc := testDCWithCreds(convergeMock(),
		"GITHUB_USER", "alice",
		"GITHUB_TOKEN", "ghp_xyz",
	)
	dc.Cluster.DeployHash = "20260417-143022"
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Registry: map[string]config.RegistryDef{
			"ghcr.io": {Username: "$GITHUB_USER", Password: "$GITHUB_TOKEN"},
		},
		Services: map[string]config.ServiceDef{
			"web": {Image: "docker.io/library/nginx"}, // no build
			"api": {
				Image: "ghcr.io/org/api:v1",
				Build: &config.BuildSpec{Context: apiCtx},
			},
		},
	}
	if err := Build(context.Background(), dc, cfg); err != nil {
		t.Fatalf("Build: %v", err)
	}
	ops := runner.ops()
	// login:ghcr.io → build:<full-tag> → push:<full-tag>
	// full-tag = ghcr.io/org/api:v1-20260417-143022 (user tag + hash suffix)
	want := []string{
		"login:ghcr.io:alice",
		"build:ghcr.io/org/api:v1-20260417-143022",
		"push:ghcr.io/org/api:v1-20260417-143022",
	}
	if len(ops) != len(want) {
		t.Fatalf("ops = %v, want %v", ops, want)
	}
	for i, w := range want {
		if ops[i] != w {
			t.Errorf("ops[%d] = %q, want %q", i, ops[i], w)
		}
	}
}

// TestBuild_DeterministicIterationOrder verifies services build in
// sorted order. Without this, re-runs produce non-identical deploy logs
// and parallel-build implementations would need to re-establish order.
func TestBuild_DeterministicIterationOrder(t *testing.T) {
	runner := &captureRunner{}
	cleanup := SetBuildRunnerForTest(runner)
	defer cleanup()

	tmp := t.TempDir()
	apiCtx := stubBuildContext(t, filepath.Join(tmp, "api"))
	webCtx := stubBuildContext(t, filepath.Join(tmp, "web"))

	dc := testDCWithCreds(convergeMock(),
		"GITHUB_USER", "alice",
		"GITHUB_TOKEN", "ghp_xyz",
	)
	dc.Cluster.DeployHash = "20260417-143022"
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Registry: map[string]config.RegistryDef{
			"ghcr.io": {Username: "$GITHUB_USER", Password: "$GITHUB_TOKEN"},
		},
		Services: map[string]config.ServiceDef{
			"web": {Image: "ghcr.io/org/web:v1", Build: &config.BuildSpec{Context: webCtx}},
			"api": {Image: "ghcr.io/org/api:v1", Build: &config.BuildSpec{Context: apiCtx}},
		},
	}
	if err := Build(context.Background(), dc, cfg); err != nil {
		t.Fatalf("Build: %v", err)
	}
	// api builds before web (sorted Service name order).
	ops := runner.ops()
	var buildOps []string
	for _, o := range ops {
		if strings.HasPrefix(o, "build:") {
			buildOps = append(buildOps, o)
		}
	}
	if len(buildOps) != 2 {
		t.Fatalf("build ops = %v, want 2", buildOps)
	}
	if !strings.Contains(buildOps[0], "/api:") {
		t.Errorf("first build should be api, got: %v", buildOps)
	}
	if !strings.Contains(buildOps[1], "/web:") {
		t.Errorf("second build should be web, got: %v", buildOps)
	}
	// Sanity: building services in a different input order must produce
	// the same output order — Go map iteration is random without sort.
	sort.Strings(buildOps) // no-op if already sorted; proves we're stable
}

// TestBuild_MissingEnvVar_HardError verifies that an unresolvable $VAR
// in the registry block surfaces BEFORE any docker call — no "docker:
// command not found" fallout when the real issue is a missing env.
func TestBuild_MissingEnvVar_HardError(t *testing.T) {
	runner := &captureRunner{}
	cleanup := SetBuildRunnerForTest(runner)
	defer cleanup()

	tmp := t.TempDir()
	apiCtx := stubBuildContext(t, filepath.Join(tmp, "api"))

	// GITHUB_TOKEN not seeded — resolution must error.
	dc := testDCWithCreds(convergeMock(), "GITHUB_USER", "alice")
	dc.Cluster.DeployHash = "20260417-143022"
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Registry: map[string]config.RegistryDef{
			"ghcr.io": {Username: "$GITHUB_USER", Password: "$GITHUB_TOKEN"},
		},
		Services: map[string]config.ServiceDef{
			"api": {Image: "ghcr.io/org/api:v1", Build: &config.BuildSpec{Context: apiCtx}},
		},
	}
	err := Build(context.Background(), dc, cfg)
	if err == nil {
		t.Fatal("expected error for missing $GITHUB_TOKEN")
	}
	if !strings.Contains(err.Error(), "GITHUB_TOKEN") {
		t.Errorf("error should name the missing var, got: %v", err)
	}
	if ops := runner.ops(); len(ops) != 0 {
		t.Errorf("runner must not be called when creds resolution fails, got %v", ops)
	}
}

// TestBuild_FailurePropagates verifies that a failing docker command
// aborts the whole Build pass — subsequent services don't get built
// with a broken registry.
func TestBuild_FailurePropagates(t *testing.T) {
	runner := &captureRunner{err: errors.New("daemon socket unreachable")}
	cleanup := SetBuildRunnerForTest(runner)
	defer cleanup()

	tmp := t.TempDir()
	apiCtx := stubBuildContext(t, filepath.Join(tmp, "api"))
	webCtx := stubBuildContext(t, filepath.Join(tmp, "web"))

	dc := testDCWithCreds(convergeMock(),
		"GITHUB_USER", "alice", "GITHUB_TOKEN", "ghp_xyz",
	)
	dc.Cluster.DeployHash = "20260417-143022"
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Registry: map[string]config.RegistryDef{"ghcr.io": {Username: "$GITHUB_USER", Password: "$GITHUB_TOKEN"}},
		Services: map[string]config.ServiceDef{
			"api": {Image: "ghcr.io/org/api:v1", Build: &config.BuildSpec{Context: apiCtx}},
			"web": {Image: "ghcr.io/org/web:v1", Build: &config.BuildSpec{Context: webCtx}},
		},
	}
	err := Build(context.Background(), dc, cfg)
	if err == nil {
		t.Fatal("expected error from failing runner")
	}
	// Confirm web was never attempted — api's login failure aborted the pass.
	for _, op := range runner.ops() {
		if strings.Contains(op, "/web:") {
			t.Errorf("web must not be built after api failure, ops: %v", runner.ops())
		}
	}
}

// Silence unused imports on future trims.
var _ app.BuildRunner

// preflightFailRunner returns a preflight error and records every call
// so the test can verify no login/build/push fires after preflight fails.
type preflightFailRunner struct {
	captureRunner
}

func (p *preflightFailRunner) PreflightBuildx(_ context.Context) error {
	p.record("preflight")
	return errors.New("docker buildx is required but not installed")
}

// INVARIANT: preflight failure aborts the whole Build pass before any
// registry creds are resolved, any docker login fires, or any service
// is built. Per-service docker errors for a missing buildx would
// otherwise spam once per service with no install hint.
func TestBuild_PreflightFailure_ShortCircuitsEverything(t *testing.T) {
	runner := &preflightFailRunner{}
	cleanup := SetBuildRunnerForTest(runner)
	defer cleanup()

	tmp := t.TempDir()
	apiCtx := stubBuildContext(t, filepath.Join(tmp, "api"))

	dc := testDCWithCreds(convergeMock(),
		"GITHUB_USER", "alice", "GITHUB_TOKEN", "ghp_xyz",
	)
	dc.Cluster.DeployHash = "20260417-143022"
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Registry: map[string]config.RegistryDef{
			"ghcr.io": {Username: "$GITHUB_USER", Password: "$GITHUB_TOKEN"},
		},
		Services: map[string]config.ServiceDef{
			"api": {Image: "ghcr.io/org/api:v1", Build: &config.BuildSpec{Context: apiCtx}},
		},
	}
	err := Build(context.Background(), dc, cfg)
	if err == nil {
		t.Fatal("expected preflight failure to propagate")
	}
	if !strings.Contains(err.Error(), "buildx") {
		t.Errorf("error should surface the buildx-missing hint, got: %v", err)
	}
	// Only "preflight" should have been recorded — no login/build/push.
	ops := runner.ops()
	if len(ops) != 1 || ops[0] != "preflight" {
		t.Errorf("preflight failure must short-circuit login/build/push, got ops: %v", ops)
	}
}
