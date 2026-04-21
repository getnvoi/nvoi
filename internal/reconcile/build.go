package reconcile

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/internal/config"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// defaultBuildRunner is the real docker CLI runner. Overridable in tests
// via SetBuildRunnerForTest so we never shell out during `bin/test`.
var defaultBuildRunner app.BuildRunner = app.DockerRunner{}

// SetBuildRunnerForTest swaps in a mock BuildRunner. Returns a cleanup
// that restores the production DockerRunner.
func SetBuildRunnerForTest(r app.BuildRunner) func() {
	orig := defaultBuildRunner
	defaultBuildRunner = r
	return func() { defaultBuildRunner = orig }
}

// Build walks cfg.Services in sorted order and, for each service with a
// non-empty `build:` block, runs docker login → build → push using the
// resolved registry credentials from cfg.Registry.
//
// Runs pre-infra (before ServersAdd) — a build failure must not leave
// half-provisioned servers behind. No k8s or SSH contact; the only
// external dependency is `docker` on the operator's PATH.
//
// Services without build: set are skipped — they pull the image from
// wherever their `image:` tag points.
// Build walks cfg.Services in sorted order and, for each service with a
// non-empty `build:` block, runs docker login → build → push using the
// resolved registry credentials from cfg.Registry.
//
// platform ("linux/amd64" or "linux/arm64") is stamped on every
// docker buildx build invocation so the image arch matches the target
// server. Derived by the caller from infra.ArchForType(masterServerType).
//
// Runs pre-Bootstrap (before ServersAdd) — a build failure must not leave
// half-provisioned servers behind. No k8s or SSH contact; the only
// external dependency is `docker` on the operator's PATH.
//
// Services without build: set are skipped — they pull the image from
// wherever their `image:` tag points.
func Build(ctx context.Context, dc *config.DeployContext, cfg *config.AppConfig, platform string) error {
	// Fast exit: no service has a build: directive. Skip the whole pass,
	// don't even require docker on PATH.
	anyBuild := false
	for _, svc := range cfg.Services {
		if svc.Build != nil && svc.Build.Context != "" {
			anyBuild = true
			break
		}
	}
	if !anyBuild {
		return nil
	}

	// Platform must be known before building any image. An empty platform
	// means the caller failed to derive the server arch — proceeding would
	// silently build for the host arch, producing an image that fails to
	// run on a cross-arch target (e.g. amd64 image on an arm64 cax11).
	if platform == "" {
		return fmt.Errorf("build: target platform unknown — cannot determine server architecture")
	}

	// Verify buildx before anything else. Per-service docker errors for
	// a missing plugin would fire N times with no install hint — this
	// single preflight gives the operator one clear, actionable error
	// before we resolve a single credential.
	if err := defaultBuildRunner.PreflightBuildx(ctx); err != nil {
		return fmt.Errorf("build: %w", err)
	}

	// Resolve registry creds once for the whole pass. Same helper the
	// Registries() step uses — missing env var errors surface here,
	// before any build starts.
	creds, err := resolveRegistries(dc, cfg)
	if err != nil {
		return fmt.Errorf("build: %w", err)
	}

	// Deterministic order — same service always builds first across
	// re-runs, so the deploy log diff is stable.
	for _, name := range utils.SortedKeys(cfg.Services) {
		svc := cfg.Services[name]
		if svc.Build == nil || svc.Build.Context == "" {
			continue
		}

		// Full ref including host + user-tag-"-"-hash (or just hash).
		// Same function Services() uses when writing the PodSpec, so the
		// registry and the cluster always agree on the image string.
		fullImage, err := ResolveImage(cfg, name, dc.Cluster.DeployHash)
		if err != nil {
			return err
		}
		host := imageRegistryHost(fullImage)
		auth, ok := creds[host]
		if !ok {
			// Validation already catches this — belt-and-suspenders in
			// case someone edits config between validate and build.
			return fmt.Errorf("services.%s.build: no credentials for registry %q", name, host)
		}

		if err := app.BuildService(ctx, app.BuildServiceRequest{
			Cluster:    dc.Cluster,
			Name:       name,
			Image:      fullImage,
			Context:    svc.Build.Context,
			Dockerfile: svc.Build.Dockerfile, // empty → BuildService defaults to <Context>/Dockerfile
			Host:       host,
			Username:   auth.Username,
			Password:   auth.Password,
			Platform:   platform,
			Runner:     defaultBuildRunner,
		}); err != nil {
			return err
		}
	}

	return nil
}
