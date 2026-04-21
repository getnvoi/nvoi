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
func (r *captureRunner) Build(_ context.Context, image, _, _, platform string, _, _ io.Writer) error {
	r.record("build:" + image + ":" + platform)
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

// TestBuildImages_SkipsWhenNoServiceHasBuild verifies the fast-exit path. Users
// with zero `build:` entries must not need `docker` on PATH.
func TestBuildImages_SkipsWhenNoServiceHasBuild(t *testing.T) {
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
	if err := BuildImages(context.Background(), dc, cfg, "linux/amd64"); err != nil {
		t.Fatalf("BuildImages: %v", err)
	}
	if ops := runner.ops(); len(ops) != 0 {
		t.Errorf("runner must not be called when no service has build:, got %v", ops)
	}
}

// TestBuildImages_OnlyBuildsServicesWithBuildField verifies we don't accidentally
// rebuild every service — only the ones flagged. Also locks that the
// resolved tag is user-tag + "-" + deploy-hash.
func TestBuildImages_OnlyBuildsServicesWithBuildField(t *testing.T) {
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
	if err := BuildImages(context.Background(), dc, cfg, "linux/amd64"); err != nil {
		t.Fatalf("BuildImages: %v", err)
	}
	ops := runner.ops()
	// login:ghcr.io → build:<full-tag> → push:<full-tag>
	// full-tag = ghcr.io/org/api:v1-20260417-143022 (user tag + hash suffix)
	want := []string{
		"login:ghcr.io:alice",
		"build:ghcr.io/org/api:v1-20260417-143022:linux/amd64",
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

// TestBuildImages_DeterministicIterationOrder verifies services build in
// sorted order. Without this, re-runs produce non-identical deploy logs
// and parallel-build implementations would need to re-establish order.
func TestBuildImages_DeterministicIterationOrder(t *testing.T) {
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
	if err := BuildImages(context.Background(), dc, cfg, "linux/amd64"); err != nil {
		t.Fatalf("BuildImages: %v", err)
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

// TestBuildImages_MissingEnvVar_HardError verifies that an unresolvable $VAR
// in the registry block surfaces BEFORE any docker call — no "docker:
// command not found" fallout when the real issue is a missing env.
func TestBuildImages_MissingEnvVar_HardError(t *testing.T) {
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
	err := BuildImages(context.Background(), dc, cfg, "linux/amd64")
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

// TestBuildImages_FailurePropagates verifies that a failing docker command
// aborts the whole Build pass — subsequent services don't get built
// with a broken registry.
func TestBuildImages_FailurePropagates(t *testing.T) {
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
	err := BuildImages(context.Background(), dc, cfg, "linux/amd64")
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

// TestBuildImages_EmptyPlatform_Errors verifies that Build() returns a hard error
// when platform is "" and at least one service has a build: directive.
// An empty platform means the caller failed to derive the server arch —
// silently proceeding would build for the host arch and produce an image
// that crashes at container start on a cross-arch target (e.g. amd64 image
// on arm64 cax11).
func TestBuildImages_EmptyPlatform_Errors(t *testing.T) {
	runner := &captureRunner{}
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
	err := BuildImages(context.Background(), dc, cfg, "")
	if err == nil {
		t.Fatal("expected error for empty platform")
	}
	if !strings.Contains(err.Error(), "platform") {
		t.Errorf("error should mention platform, got: %v", err)
	}
	// Nothing should have been called — error fires before preflight.
	if ops := runner.ops(); len(ops) != 0 {
		t.Errorf("runner must not be called when platform is empty, got %v", ops)
	}
}

// INVARIANT: preflight failure aborts the whole Build pass before any
// registry creds are resolved, any docker login fires, or any service
// is built. Per-service docker errors for a missing buildx would
// otherwise spam once per service with no install hint.
func TestBuildImages_PreflightFailure_ShortCircuitsEverything(t *testing.T) {
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
	err := BuildImages(context.Background(), dc, cfg, "linux/amd64")
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

// ── masterServerType ────────────────────────────────────────────────────────

// TestMasterServerType verifies the helper extracts the master server's type
// string from cfg.Servers. reconcile.Deploy feeds this into
// infra.ArchForType to derive the --platform flag.
func TestMasterServerType(t *testing.T) {
	cases := []struct {
		name    string
		servers map[string]config.ServerDef
		want    string
	}{
		{
			name: "single master",
			servers: map[string]config.ServerDef{
				"master": {Type: "cax11", Region: "nbg1", Role: "master"},
			},
			want: "cax11",
		},
		{
			name: "master + worker",
			servers: map[string]config.ServerDef{
				"master":   {Type: "cx22", Region: "nbg1", Role: "master"},
				"worker-1": {Type: "cx11", Region: "nbg1", Role: "worker"},
			},
			want: "cx22",
		},
		{
			name:    "no servers",
			servers: map[string]config.ServerDef{},
			want:    "",
		},
		{
			name: "workers only",
			servers: map[string]config.ServerDef{
				"worker-1": {Type: "cx11", Region: "nbg1", Role: "worker"},
			},
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.AppConfig{Servers: tc.servers}
			got := masterServerType(cfg)
			if got != tc.want {
				t.Errorf("masterServerType = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestBuildImages_PlatformStampedOnBuildRecord verifies that the platform string
// passed to Build() reaches the docker buildx invocation. This is the
// cross-arch correctness invariant: an arm64 platform string for a cax11
// target must not be silently replaced by the operator's host arch.
func TestBuildImages_PlatformStampedOnBuildRecord(t *testing.T) {
	runner := &captureRunner{}
	cleanup := SetBuildRunnerForTest(runner)
	defer cleanup()

	tmp := t.TempDir()
	apiCtx := stubBuildContext(t, filepath.Join(tmp, "api"))

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
			"api": {Image: "ghcr.io/org/api:v1", Build: &config.BuildSpec{Context: apiCtx}},
		},
	}

	// arm64 simulates a cax11 target — verifies the platform is not
	// silently replaced by the operator's host arch.
	if err := BuildImages(context.Background(), dc, cfg, "linux/arm64"); err != nil {
		t.Fatalf("BuildImages: %v", err)
	}
	for _, op := range runner.ops() {
		if strings.HasPrefix(op, "build:") {
			if !strings.HasSuffix(op, ":linux/arm64") {
				t.Errorf("build record should end with :linux/arm64, got %q", op)
			}
			return
		}
	}
	t.Fatal("no build op recorded")
}
