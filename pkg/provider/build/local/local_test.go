package local

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/provider"
)

// TestLocalBuilder_RegistersOnImport verifies init() wires "local" into the
// build registry with the static capability bits the validator depends on.
// R1 (pkg/provider/CLAUDE.md + internal/reconcile/validate.go) gates on
// RequiresBuilders; regressing either bit here silently opens the door to
// configs the validator is supposed to reject. cmd/cli/main.go relies on a
// blank import to trigger this init — no explicit setup in the test.
func TestLocalBuilder_RegistersOnImport(t *testing.T) {
	if _, err := provider.GetBuildSchema("local"); err != nil {
		t.Fatalf("local build provider not registered: %v", err)
	}
	caps, err := provider.GetBuildCapability("local")
	if err != nil {
		t.Fatalf("GetBuildCapability(local): %v", err)
	}
	if caps.RequiresBuilders {
		t.Errorf("local.RequiresBuilders: got true, want false (local runs in-process, consumes zero role:builder servers)")
	}
}

// TestLocalBuilder_ResolveReturnsInstance verifies ResolveBuild round-trips
// through the factory. Local takes no credentials, so an empty map must
// succeed and Close() must be a clean no-op.
func TestLocalBuilder_ResolveReturnsInstance(t *testing.T) {
	b, err := provider.ResolveBuild("local", nil)
	if err != nil {
		t.Fatalf("ResolveBuild(local): %v", err)
	}
	if b == nil {
		t.Fatal("ResolveBuild(local) returned nil provider")
	}
	if err := b.Close(); err != nil {
		t.Errorf("Close: got %v, want nil", err)
	}
}

// captureRunner records every BuildRunner call in order. Mirrors
// pkg/core/build_test.go::fakeBuildRunner but kept local to avoid coupling
// test packages — local only needs to prove the BuildRequest values land on
// the right runner method in the right sequence.
type captureRunner struct {
	ops     []string
	preflgt int
	login   struct{ host, user, pass string }
	build   struct{ image, ctx, dockerfile, platform string }
	push    struct{ image string }
}

func (r *captureRunner) PreflightBuildx(context.Context) error {
	r.preflgt++
	r.ops = append(r.ops, "preflight")
	return nil
}
func (r *captureRunner) Login(_ context.Context, host, user, pass string) error {
	r.login.host, r.login.user, r.login.pass = host, user, pass
	r.ops = append(r.ops, "login")
	return nil
}
func (r *captureRunner) Build(_ context.Context, image, ctx, dockerfile, platform string, _, _ io.Writer) error {
	r.build.image, r.build.ctx, r.build.dockerfile, r.build.platform = image, ctx, dockerfile, platform
	r.ops = append(r.ops, "build")
	return nil
}
func (r *captureRunner) Push(_ context.Context, image string, _, _ io.Writer) error {
	r.push.image = image
	r.ops = append(r.ops, "push")
	return nil
}

// writeDockerfile creates a minimal context dir with a Dockerfile so the
// BuildService os.Stat pre-check in pkg/core/build.go passes.
func writeDockerfile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestLocalBuilder_Build_DelegatesToBuildService verifies the happy path:
// Build maps BuildRequest onto BuildRunner (preflight→login→build→push in
// that exact order), returns req.Image unchanged (local never rewrites the
// tag — content-addressed rewriting is a depot-era concern), and writes
// progress lines to req.Output.
func TestLocalBuilder_Build_DelegatesToBuildService(t *testing.T) {
	rnr := &captureRunner{}
	var out bytes.Buffer
	b := newWithRunner(rnr)

	ctxDir := writeDockerfile(t)
	ref, err := b.Build(context.Background(), provider.BuildRequest{
		Service:    "api",
		Context:    ctxDir,
		Dockerfile: "", // default to <Context>/Dockerfile
		Platform:   "linux/arm64",
		Image:      "ghcr.io/org/api:v1-20260101-000000",
		Registry:   provider.RegistryAuth{Host: "ghcr.io", Username: "u", Password: "p"},
		Output:     &out,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if ref != "ghcr.io/org/api:v1-20260101-000000" {
		t.Errorf("ref: got %q, want req.Image verbatim (local must not rewrite)", ref)
	}

	wantOps := []string{"preflight", "login", "build", "push"}
	if got := strings.Join(rnr.ops, ","); got != strings.Join(wantOps, ",") {
		t.Errorf("op order: got %v, want %v", rnr.ops, wantOps)
	}
	if rnr.login.host != "ghcr.io" || rnr.login.user != "u" || rnr.login.pass != "p" {
		t.Errorf("login args: got %+v", rnr.login)
	}
	if rnr.build.image != "ghcr.io/org/api:v1-20260101-000000" {
		t.Errorf("build image: got %q", rnr.build.image)
	}
	if rnr.build.platform != "linux/arm64" {
		t.Errorf("build platform: got %q, want linux/arm64 (derived from master arch, MUST flow through)", rnr.build.platform)
	}
	if rnr.push.image != rnr.build.image {
		t.Errorf("push image %q != build image %q", rnr.push.image, rnr.build.image)
	}
	if out.Len() == 0 {
		t.Errorf("expected progress lines on req.Output, got empty buffer")
	}
}

// TestLocalBuilder_Build_PreflightOncePerInstance locks the contract that
// reconcile.BuildImages constructs one LocalBuilder and loops Build(N) over
// every service with a `build:` directive — the preflight cost must not
// scale with service count. PreflightBuildx is the only runner method we
// deduplicate; login/build/push run per-service by design.
func TestLocalBuilder_Build_PreflightOncePerInstance(t *testing.T) {
	rnr := &captureRunner{}
	b := newWithRunner(rnr)
	ctxDir := writeDockerfile(t)
	req := provider.BuildRequest{
		Service:  "api",
		Context:  ctxDir,
		Platform: "linux/amd64",
		Image:    "ghcr.io/org/api:v1",
		Registry: provider.RegistryAuth{Host: "ghcr.io", Username: "u", Password: "p"},
		Output:   io.Discard,
	}
	for i := 0; i < 3; i++ {
		if _, err := b.Build(context.Background(), req); err != nil {
			t.Fatalf("Build #%d: %v", i, err)
		}
	}
	if rnr.preflgt != 1 {
		t.Errorf("preflight calls across 3 Builds: got %d, want 1", rnr.preflgt)
	}
}

// TestLocalBuilder_Build_RejectsEmptyRequiredFields locks the actionable
// error messages for caller bugs. Each field is validated independently —
// a silent default for Platform would build for the operator's host arch
// and ship a broken image to a cross-arch cluster.
func TestLocalBuilder_Build_RejectsEmptyRequiredFields(t *testing.T) {
	b := newWithRunner(&captureRunner{})
	ctxDir := writeDockerfile(t)

	cases := []struct {
		name string
		req  provider.BuildRequest
		want string
	}{
		{"no image", provider.BuildRequest{Service: "api", Context: ctxDir, Platform: "linux/amd64"}, "Image is required"},
		{"no context", provider.BuildRequest{Service: "api", Platform: "linux/amd64", Image: "x:y"}, "Context is required"},
		{"no platform", provider.BuildRequest{Service: "api", Context: ctxDir, Image: "x:y"}, "Platform is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := b.Build(context.Background(), tc.req)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got err=%v, want substring %q", err, tc.want)
			}
		})
	}
}

// Ensure the outputAdapter satisfies core.Output. Compile-time assertion —
// no test body needed; the declaration below fails to compile if the
// adapter drifts out of sync with the Output interface.
var _ app.Output = outputAdapter{}
