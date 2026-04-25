package reconcile

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/pkg/provider"
)

// captureBuilder is a BuildProvider test double that records every Build
// call for assertions. Registered via registerCaptureBuilder under a
// per-test name so reconcile.BuildImages resolves it through the
// production provider registry — no private test seam.
//
// The BuildRequest is captured verbatim, so each test can assert on
// Service / Image / Platform / Registry / Builders / GitRemote / GitRef
// with field-level precision. Error mode is controlled by setting
// captureBuilder.err — the builder returns that error from Build, which
// propagates up through BuildImages.
type captureBuilder struct {
	mu    sync.Mutex
	calls []provider.BuildRequest
	err   error
}

func (c *captureBuilder) Build(_ context.Context, req provider.BuildRequest) (string, error) {
	c.mu.Lock()
	c.calls = append(c.calls, req)
	c.mu.Unlock()
	if c.err != nil {
		return "", c.err
	}
	return req.Image, nil
}

func (c *captureBuilder) Close() error { return nil }

func (c *captureBuilder) requests() []provider.BuildRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]provider.BuildRequest, len(c.calls))
	copy(out, c.calls)
	return out
}

// registerCaptureBuilder installs a captureBuilder under `name` with the
// given capability bits. Returns the builder so the caller can assert on
// its recorded calls. Credential schema is empty — tests that need
// credentials set them via dc.Creds directly (registry auth resolution).
//
// RegisterBuild replaces on duplicate name, so each test registers under
// a unique name to avoid cross-test interference when tests are reordered.
func registerCaptureBuilder(_ *testing.T, name string, caps provider.BuildCapability) *captureBuilder {
	b := &captureBuilder{}
	provider.RegisterBuild(name, provider.CredentialSchema{}, caps, func(map[string]string) provider.BuildProvider {
		return b
	})
	return b
}

// TestBuildImages_SkipsWhenNoServiceHasBuild verifies the fast-exit path. Users
// with zero `build:` entries must not need any BuildProvider on PATH —
// BuildImages returns before even resolving one.
func TestBuildImages_SkipsWhenNoServiceHasBuild(t *testing.T) {
	b := registerCaptureBuilder(t, "test-capture-skip", provider.BuildCapability{})

	dc := testDCWithCreds(convergeMock())
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Providers: config.ProvidersDef{Build: "test-capture-skip"},
		Services: map[string]config.ServiceDef{
			"web": {Image: "docker.io/library/nginx"},
			"api": {Image: "docker.io/library/redis"},
		},
	}
	if err := BuildImages(context.Background(), dc, cfg, "linux/amd64", nil); err != nil {
		t.Fatalf("BuildImages: %v", err)
	}
	if reqs := b.requests(); len(reqs) != 0 {
		t.Errorf("builder must not be called when no service has build:, got %d requests", len(reqs))
	}
}

// TestBuildImages_OnlyBuildsServicesWithBuildField verifies we don't accidentally
// rebuild every service — only the ones flagged. Also locks that the
// resolved image tag is user-tag + "-" + deploy-hash AND that resolved
// registry credentials land on BuildRequest.Registry verbatim.
func TestBuildImages_OnlyBuildsServicesWithBuildField(t *testing.T) {
	b := registerCaptureBuilder(t, "test-capture-only", provider.BuildCapability{})

	dc := testDCWithCreds(convergeMock(),
		"GITHUB_USER", "alice",
		"GITHUB_TOKEN", "ghp_xyz",
	)
	dc.Cluster.DeployHash = "20260417-143022"
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Providers: config.ProvidersDef{Build: "test-capture-only"},
		Registry: map[string]config.RegistryDef{
			"ghcr.io": {Username: "$GITHUB_USER", Password: "$GITHUB_TOKEN"},
		},
		Services: map[string]config.ServiceDef{
			"web": {Image: "docker.io/library/nginx"}, // no build — skipped
			"api": {
				Image: "ghcr.io/org/api:v1",
				Build: &config.BuildSpec{Context: "./services/api"},
			},
		},
	}
	if err := BuildImages(context.Background(), dc, cfg, "linux/amd64", nil); err != nil {
		t.Fatalf("BuildImages: %v", err)
	}
	reqs := b.requests()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 build request (only api has build:), got %d", len(reqs))
	}
	got := reqs[0]
	if got.Service != "api" {
		t.Errorf("Service = %q, want api", got.Service)
	}
	if want := "ghcr.io/org/api:v1-20260417-143022"; got.Image != want {
		t.Errorf("Image = %q, want %q (user tag + '-' + deploy hash)", got.Image, want)
	}
	if got.Platform != "linux/amd64" {
		t.Errorf("Platform = %q, want linux/amd64", got.Platform)
	}
	if got.Context != "./services/api" {
		t.Errorf("Context = %q, want ./services/api", got.Context)
	}
	if got.Registry.Host != "ghcr.io" || got.Registry.Username != "alice" || got.Registry.Password != "ghp_xyz" {
		t.Errorf("Registry = %+v, want {Host:ghcr.io Username:alice Password:ghp_xyz}", got.Registry)
	}
}

// TestBuildImages_DeterministicIterationOrder verifies services build in
// sorted order. Without this, re-runs produce non-identical deploy logs
// and any parallel-build implementation would need to re-establish order.
func TestBuildImages_DeterministicIterationOrder(t *testing.T) {
	b := registerCaptureBuilder(t, "test-capture-order", provider.BuildCapability{})

	dc := testDCWithCreds(convergeMock(),
		"GITHUB_USER", "alice",
		"GITHUB_TOKEN", "ghp_xyz",
	)
	dc.Cluster.DeployHash = "20260417-143022"
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Providers: config.ProvidersDef{Build: "test-capture-order"},
		Registry: map[string]config.RegistryDef{
			"ghcr.io": {Username: "$GITHUB_USER", Password: "$GITHUB_TOKEN"},
		},
		Services: map[string]config.ServiceDef{
			"web": {Image: "ghcr.io/org/web:v1", Build: &config.BuildSpec{Context: "./web"}},
			"api": {Image: "ghcr.io/org/api:v1", Build: &config.BuildSpec{Context: "./api"}},
		},
	}
	if err := BuildImages(context.Background(), dc, cfg, "linux/amd64", nil); err != nil {
		t.Fatalf("BuildImages: %v", err)
	}
	reqs := b.requests()
	if len(reqs) != 2 {
		t.Fatalf("expected 2 build requests, got %d", len(reqs))
	}
	// api sorts before web in lexicographic service order.
	if reqs[0].Service != "api" {
		t.Errorf("first build should be api, got %q", reqs[0].Service)
	}
	if reqs[1].Service != "web" {
		t.Errorf("second build should be web, got %q", reqs[1].Service)
	}
}

// TestBuildImages_MissingEnvVar_HardError verifies that an unresolvable $VAR
// in the registry block surfaces BEFORE any BuildProvider call — no "docker:
// command not found" / "ssh: connection refused" fallout when the real
// issue is a missing env var.
func TestBuildImages_MissingEnvVar_HardError(t *testing.T) {
	b := registerCaptureBuilder(t, "test-capture-missing", provider.BuildCapability{})

	// GITHUB_TOKEN not seeded — registry auth resolution must error.
	dc := testDCWithCreds(convergeMock(), "GITHUB_USER", "alice")
	dc.Cluster.DeployHash = "20260417-143022"
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Providers: config.ProvidersDef{Build: "test-capture-missing"},
		Registry: map[string]config.RegistryDef{
			"ghcr.io": {Username: "$GITHUB_USER", Password: "$GITHUB_TOKEN"},
		},
		Services: map[string]config.ServiceDef{
			"api": {Image: "ghcr.io/org/api:v1", Build: &config.BuildSpec{Context: "./api"}},
		},
	}
	err := BuildImages(context.Background(), dc, cfg, "linux/amd64", nil)
	if err == nil {
		t.Fatal("expected error for missing $GITHUB_TOKEN")
	}
	if !strings.Contains(err.Error(), "GITHUB_TOKEN") {
		t.Errorf("error should name the missing var, got: %v", err)
	}
	if reqs := b.requests(); len(reqs) != 0 {
		t.Errorf("builder must not be called when creds resolution fails, got %d requests", len(reqs))
	}
}

// TestBuildImages_FailurePropagates verifies that a failing BuildProvider
// aborts the whole Build pass — subsequent services don't get built with
// a broken upstream.
func TestBuildImages_FailurePropagates(t *testing.T) {
	b := registerCaptureBuilder(t, "test-capture-fail", provider.BuildCapability{})
	b.err = errors.New("builder exploded")

	dc := testDCWithCreds(convergeMock(),
		"GITHUB_USER", "alice", "GITHUB_TOKEN", "ghp_xyz",
	)
	dc.Cluster.DeployHash = "20260417-143022"
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Providers: config.ProvidersDef{Build: "test-capture-fail"},
		Registry: map[string]config.RegistryDef{
			"ghcr.io": {Username: "$GITHUB_USER", Password: "$GITHUB_TOKEN"},
		},
		Services: map[string]config.ServiceDef{
			"api": {Image: "ghcr.io/org/api:v1", Build: &config.BuildSpec{Context: "./api"}},
			"web": {Image: "ghcr.io/org/web:v1", Build: &config.BuildSpec{Context: "./web"}},
		},
	}
	err := BuildImages(context.Background(), dc, cfg, "linux/amd64", nil)
	if err == nil {
		t.Fatal("expected error from failing builder")
	}
	// api sorts first, fails first; web must never reach the builder.
	reqs := b.requests()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request (api; web skipped after failure), got %d", len(reqs))
	}
	if reqs[0].Service != "api" {
		t.Errorf("first (and only) request should be api, got %q", reqs[0].Service)
	}
}

// TestBuildImages_EmptyPlatform_Errors verifies that BuildImages returns a hard
// error when platform is "" and at least one service has a build: directive.
// An empty platform means the caller failed to derive the server arch —
// silently proceeding would build for the host arch and produce an image
// that crashes at container start on a cross-arch target (e.g. amd64 image
// on arm64 cax11).
func TestBuildImages_EmptyPlatform_Errors(t *testing.T) {
	b := registerCaptureBuilder(t, "test-capture-platform", provider.BuildCapability{})

	dc := testDCWithCreds(convergeMock(),
		"GITHUB_USER", "alice", "GITHUB_TOKEN", "ghp_xyz",
	)
	dc.Cluster.DeployHash = "20260417-143022"
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Providers: config.ProvidersDef{Build: "test-capture-platform"},
		Registry: map[string]config.RegistryDef{
			"ghcr.io": {Username: "$GITHUB_USER", Password: "$GITHUB_TOKEN"},
		},
		Services: map[string]config.ServiceDef{
			"api": {Image: "ghcr.io/org/api:v1", Build: &config.BuildSpec{Context: "./api"}},
		},
	}
	err := BuildImages(context.Background(), dc, cfg, "", nil)
	if err == nil {
		t.Fatal("expected error for empty platform")
	}
	if !strings.Contains(err.Error(), "platform") {
		t.Errorf("error should mention platform, got: %v", err)
	}
	if reqs := b.requests(); len(reqs) != 0 {
		t.Errorf("builder must not be called when platform is empty, got %d requests", len(reqs))
	}
}

// TestBuildImages_PlatformStampedOnBuildRequest verifies that the platform
// string passed to BuildImages reaches the BuildProvider via
// BuildRequest.Platform. This is the cross-arch correctness invariant: an
// arm64 platform string for a cax11 target must not be silently replaced
// by the operator's host arch downstream.
func TestBuildImages_PlatformStampedOnBuildRequest(t *testing.T) {
	b := registerCaptureBuilder(t, "test-capture-arm", provider.BuildCapability{})

	dc := testDCWithCreds(convergeMock(),
		"GITHUB_USER", "alice",
		"GITHUB_TOKEN", "ghp_xyz",
	)
	dc.Cluster.DeployHash = "20260417-143022"
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Providers: config.ProvidersDef{Build: "test-capture-arm"},
		Registry: map[string]config.RegistryDef{
			"ghcr.io": {Username: "$GITHUB_USER", Password: "$GITHUB_TOKEN"},
		},
		Services: map[string]config.ServiceDef{
			"api": {Image: "ghcr.io/org/api:v1", Build: &config.BuildSpec{Context: "./api"}},
		},
	}

	// arm64 simulates a cax11 target.
	if err := BuildImages(context.Background(), dc, cfg, "linux/arm64", nil); err != nil {
		t.Fatalf("BuildImages: %v", err)
	}
	reqs := b.requests()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(reqs))
	}
	if reqs[0].Platform != "linux/arm64" {
		t.Errorf("Platform = %q, want linux/arm64", reqs[0].Platform)
	}
}

// TestBuildImages_BuildersPassedToProvider verifies the builders slice
// (produced by infra.BuilderTargets for RequiresBuilders=true providers)
// reaches the BuildProvider via BuildRequest.Builders. The ssh
// BuildProvider consumes this; local/daytona ignore it. Locked here so
// a future refactor can't silently drop the wiring.
func TestBuildImages_BuildersPassedToProvider(t *testing.T) {
	b := registerCaptureBuilder(t, "test-capture-builders", provider.BuildCapability{RequiresBuilders: true})

	dc := testDCWithCreds(convergeMock(),
		"GITHUB_USER", "alice", "GITHUB_TOKEN", "ghp_xyz",
	)
	dc.Cluster.DeployHash = "20260417-143022"
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Providers: config.ProvidersDef{Build: "test-capture-builders"},
		Registry: map[string]config.RegistryDef{
			"ghcr.io": {Username: "$GITHUB_USER", Password: "$GITHUB_TOKEN"},
		},
		Services: map[string]config.ServiceDef{
			"api": {Image: "ghcr.io/org/api:v1", Build: &config.BuildSpec{Context: "./api"}},
		},
	}
	builders := []provider.BuilderTarget{
		{Name: "nvoi-myapp-prod-builder-1", Host: "1.2.3.4", User: "deploy"},
	}
	if err := BuildImages(context.Background(), dc, cfg, "linux/amd64", builders); err != nil {
		t.Fatalf("BuildImages: %v", err)
	}
	reqs := b.requests()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(reqs))
	}
	got := reqs[0].Builders
	if len(got) != 1 || got[0].Name != "nvoi-myapp-prod-builder-1" || got[0].Host != "1.2.3.4" || got[0].User != "deploy" {
		t.Errorf("BuildRequest.Builders = %+v, want [{Name:nvoi-myapp-prod-builder-1 Host:1.2.3.4 User:deploy}]", got)
	}
}

// TestBuildImages_GitRefCarriedOnRequest verifies GitRemote + GitRef
// (populated by cmd/cli/deploy.go from the operator's cwd) reach the
// BuildProvider. Remote builders (ssh, daytona) need these to clone the
// exact tree being deployed; local ignores them. Locked so the CLI-layer
// git inference can't be accidentally severed from the request.
func TestBuildImages_GitRefCarriedOnRequest(t *testing.T) {
	b := registerCaptureBuilder(t, "test-capture-gitref", provider.BuildCapability{})

	dc := testDCWithCreds(convergeMock(),
		"GITHUB_USER", "alice", "GITHUB_TOKEN", "ghp_xyz",
	)
	dc.Cluster.DeployHash = "20260417-143022"
	dc.GitRemote = "git@github.com:getnvoi/nvoi.git"
	dc.GitRef = "abcdef1234567890abcdef1234567890abcdef12"

	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Providers: config.ProvidersDef{Build: "test-capture-gitref"},
		Registry: map[string]config.RegistryDef{
			"ghcr.io": {Username: "$GITHUB_USER", Password: "$GITHUB_TOKEN"},
		},
		Services: map[string]config.ServiceDef{
			"api": {Image: "ghcr.io/org/api:v1", Build: &config.BuildSpec{Context: "./api"}},
		},
	}
	if err := BuildImages(context.Background(), dc, cfg, "linux/amd64", nil); err != nil {
		t.Fatalf("BuildImages: %v", err)
	}
	reqs := b.requests()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(reqs))
	}
	if reqs[0].GitRemote != dc.GitRemote {
		t.Errorf("GitRemote = %q, want %q", reqs[0].GitRemote, dc.GitRemote)
	}
	if reqs[0].GitRef != dc.GitRef {
		t.Errorf("GitRef = %q, want %q", reqs[0].GitRef, dc.GitRef)
	}
}

// TestBuildImages_EmptyImageRef_Errors verifies the contract defense: a
// BuildProvider that returns ("", nil) violates the Build contract (the
// ref is what Services() stamps on the PodSpec). BuildImages must surface
// this as an error, not silently propagate an empty ref.
func TestBuildImages_EmptyImageRef_Errors(t *testing.T) {
	// Custom builder that returns ("", nil) — breaks the contract.
	broken := &emptyRefBuilder{}
	provider.RegisterBuild("test-capture-emptyref", provider.CredentialSchema{},
		provider.BuildCapability{},
		func(map[string]string) provider.BuildProvider { return broken })

	dc := testDCWithCreds(convergeMock(),
		"GITHUB_USER", "alice", "GITHUB_TOKEN", "ghp_xyz",
	)
	dc.Cluster.DeployHash = "20260417-143022"
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Providers: config.ProvidersDef{Build: "test-capture-emptyref"},
		Registry: map[string]config.RegistryDef{
			"ghcr.io": {Username: "$GITHUB_USER", Password: "$GITHUB_TOKEN"},
		},
		Services: map[string]config.ServiceDef{
			"api": {Image: "ghcr.io/org/api:v1", Build: &config.BuildSpec{Context: "./api"}},
		},
	}
	err := BuildImages(context.Background(), dc, cfg, "linux/amd64", nil)
	if err == nil {
		t.Fatal("expected error for empty imageRef")
	}
	if !strings.Contains(err.Error(), "empty image ref") {
		t.Errorf("error should mention empty image ref, got: %v", err)
	}
}

type emptyRefBuilder struct{}

func (emptyRefBuilder) Build(_ context.Context, _ provider.BuildRequest) (string, error) {
	return "", nil
}
func (emptyRefBuilder) Close() error { return nil }

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
