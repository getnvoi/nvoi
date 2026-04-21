// Package local is the default BuildProvider. It shells out to
// `docker login` + `docker buildx build --push` on the operator's machine
// against the local docker daemon — the same mechanics that have driven
// the inner build pass since day one. Registered as a BuildProvider so
// `providers.build: local` is addressable symmetrically alongside `ssh`
// and `daytona`; an unset `providers.build` also defaults to "local" at
// the validator layer and in reconcile.BuildImages.
//
// Credentials: none — local holds no state of its own. The operator's
// ~/.docker/config.json is the backing store for registry auth
// (Kamal-style). Per-build login/build/push goes through
// pkg/core.BuildService + DockerRunner, so any change to the docker
// shell-out shape stays in one file (pkg/core/build.go).
package local

import (
	"context"
	"fmt"
	"io"

	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/provider"
)

// LocalBuilder is the registered BuildProvider for name "local".
type LocalBuilder struct {
	runner app.BuildRunner
	// preflightDone guards PreflightBuildx across repeated Build calls on
	// the same provider instance. reconcile.BuildImages constructs one
	// LocalBuilder and calls Build N times (one per service with a
	// build: directive); we want exactly one install-hint error on a
	// missing buildx, not N redundant checks.
	preflightDone bool
}

// New returns a LocalBuilder wired to the production DockerRunner.
func New() *LocalBuilder { return &LocalBuilder{runner: app.DockerRunner{}} }

// newWithRunner is the test seam. Tests substitute a recording runner so
// login/build/push ordering is asserted without shelling out to docker.
func newWithRunner(r app.BuildRunner) *LocalBuilder { return &LocalBuilder{runner: r} }

// Build logs in to req.Registry, runs `docker buildx build --push` with
// --platform = req.Platform, and returns req.Image on success. Local
// never rewrites the tag — digest rewriting is a content-addressed
// builder concern (future depot).
func (b *LocalBuilder) Build(ctx context.Context, req provider.BuildRequest) (string, error) {
	if req.Image == "" {
		return "", fmt.Errorf("services.%s.build: Image is required", req.Service)
	}
	if req.Context == "" {
		return "", fmt.Errorf("services.%s.build: Context is required", req.Service)
	}
	if req.Platform == "" {
		return "", fmt.Errorf("services.%s.build: Platform is required — an empty platform would silently build for the operator's host arch", req.Service)
	}

	runner := b.runner
	if runner == nil {
		runner = app.DockerRunner{}
	}

	// Preflight once per provider instance. reconcile.BuildImages reuses
	// one LocalBuilder across every service in the deploy, so without this
	// gate we'd spawn `docker buildx version` N times per deploy.
	if !b.preflightDone {
		if err := runner.PreflightBuildx(ctx); err != nil {
			return "", fmt.Errorf("build: %w", err)
		}
		b.preflightDone = true
	}

	// Delegate to BuildService for login + build + push. The synthetic
	// Cluster only needs Output for progress breadcrumbs — BuildService
	// never touches SSH or kube here.
	cluster := app.Cluster{Output: outputAdapter{w: req.Output}}
	if err := app.BuildService(ctx, app.BuildServiceRequest{
		Cluster:    cluster,
		Name:       req.Service,
		Image:      req.Image,
		Context:    req.Context,
		Dockerfile: req.Dockerfile,
		Host:       req.Registry.Host,
		Username:   req.Registry.Username,
		Password:   req.Registry.Password,
		Platform:   req.Platform,
		Runner:     runner,
	}); err != nil {
		return "", err
	}
	return req.Image, nil
}

// Close is a no-op; local holds no resources.
func (*LocalBuilder) Close() error { return nil }

// outputAdapter wraps the BuildRequest.Output io.Writer to satisfy
// pkg/core.Output. BuildService emits Progress / Success / Warning /
// Info / Error events during the build — we serialize them as plain
// text lines onto the writer so the operator sees real-time build
// output. Command events are dropped (structured UI data, not stream
// content).
type outputAdapter struct{ w io.Writer }

func (a outputAdapter) writer() io.Writer {
	if a.w == nil {
		return io.Discard
	}
	return a.w
}

func (a outputAdapter) write(s string) {
	_, _ = a.writer().Write([]byte(s))
}

func (a outputAdapter) Command(string, string, string, ...any) {}
func (a outputAdapter) Progress(s string)                      { a.write(s + "\n") }
func (a outputAdapter) Success(s string)                       { a.write(s + "\n") }
func (a outputAdapter) Warning(s string)                       { a.write("warning: " + s + "\n") }
func (a outputAdapter) Info(s string)                          { a.write(s + "\n") }
func (a outputAdapter) Error(err error)                        { a.write("error: " + err.Error() + "\n") }
func (a outputAdapter) Writer() io.Writer                      { return a.writer() }

func init() {
	provider.RegisterBuild(
		"local",
		provider.CredentialSchema{Name: "local"}, // no credentials — runs in-process
		provider.BuildCapability{
			// local never consumes role: builder servers — it builds on
			// whichever machine is running nvoi deploy. R1 (negative)
			// rejects configs that pair `local` with builder: servers.
			RequiresBuilders: false,
		},
		func(_ map[string]string) provider.BuildProvider { return New() },
	)
}
