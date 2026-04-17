package core

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// BuildRunner is the narrow surface pkg/core needs from "docker" on the
// operator's machine. Production implementation (DockerRunner) shells out
// to the real `docker` CLI; tests inject a fake that records call order +
// stdin payloads.
//
// We deliberately don't isolate the operator's `~/.docker/config.json` —
// `docker login` writes an auth entry there, `docker push` reads it back.
// Same contract as Kamal, docker-compose, and every other deploy tool:
// take the user's docker environment as-given. Earlier attempts to override
// DOCKER_CONFIG to a per-deploy tempdir broke plugin discovery (buildx
// disappears) and current-context resolution (docker falls back to
// /var/run/docker.sock and fails to connect to OrbStack/colima/Docker
// Desktop). Not worth it for the tiny auth-hygiene win.
type BuildRunner interface {
	// PreflightBuildx verifies the docker client has the buildx plugin
	// available. Called once per deploy, before any service login /
	// build / push, so missing-buildx surfaces with install instructions
	// before we bother resolving registry creds or spawning any docker
	// children for real work.
	PreflightBuildx(ctx context.Context) error
	Login(ctx context.Context, host, username, password string) error
	Build(ctx context.Context, image, context, dockerfile string, stdout, stderr io.Writer) error
	Push(ctx context.Context, image string, stdout, stderr io.Writer) error
}

// DockerRunner is the production implementation. Shells out to the
// `docker` CLI with `--password-stdin` so credentials never appear in
// argv / ps output / shell history, and `docker buildx build` (not
// `docker build`) for BuildKit support and compatibility with modern
// Dockerfile syntax.
type DockerRunner struct{}

// PreflightBuildx runs `docker buildx version` and, on failure, rewrites
// the error into an actionable install hint. Without this the operator
// sees a cryptic "'buildx' is not a docker command" mid-deploy, possibly
// per-service, with no pointer to the fix.
func (DockerRunner) PreflightBuildx(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "docker", "buildx", "version")
	cmd.Env = os.Environ()
	var combined strings.Builder
	cmd.Stdout = &combined
	cmd.Stderr = &combined
	if err := cmd.Run(); err != nil {
		return fmt.Errorf(
			"docker buildx is required to build service images but is not installed on this machine.\n\n"+
				"Install (macOS / Homebrew):\n"+
				"  brew install docker-buildx\n"+
				"  mkdir -p ~/.docker/cli-plugins\n"+
				"  ln -sfn $(brew --prefix docker-buildx)/bin/docker-buildx ~/.docker/cli-plugins/docker-buildx\n\n"+
				"Install (other platforms): https://docs.docker.com/go/buildx/\n\n"+
				"Underlying docker error: %s",
			strings.TrimSpace(combined.String()),
		)
	}
	return nil
}

func (DockerRunner) Login(ctx context.Context, host, username, password string) error {
	cmd := exec.CommandContext(ctx, "docker", "login", host, "-u", username, "--password-stdin")
	cmd.Env = os.Environ()
	cmd.Stdin = strings.NewReader(password)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	// Swallow stdout — docker login prints "Login Succeeded" or auth
	// warnings we don't want leaking into deploy output.
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker login %s: %w: %s", host, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func (DockerRunner) Build(ctx context.Context, image, buildCtx, dockerfile string, stdout, stderr io.Writer) error {
	// `docker buildx build` is the modern canonical path, same as
	// Kamal and rbrun. Explicit over `docker build` because:
	//   - Supports BuildKit-only Dockerfile features (cache mounts,
	//     `# syntax=...`, COPY --chmod) that the legacy builder rejects.
	//   - `--progress=plain` is a buildx flag — the legacy builder
	//     errors with "unknown flag: --progress" before even reading
	//     the Dockerfile.
	//   - If buildx is missing, PreflightBuildx catches it with a
	//     clean install-hint error before we get here.
	//
	// No `--load` flag: with the default `docker` driver buildx writes
	// the image into the local docker image store automatically.
	// `docker push` in the next step reads from that store.
	args := []string{
		"buildx", "build",
		"-t", image,
		"-f", dockerfile,
		"--progress=plain",
		buildCtx,
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Env = os.Environ()
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker buildx build %s: %w", image, err)
	}
	return nil
}

func (DockerRunner) Push(ctx context.Context, image string, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, "docker", "push", image)
	cmd.Env = os.Environ()
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker push %s: %w", image, err)
	}
	return nil
}

// BuildServiceRequest wraps one service's build-and-push.
type BuildServiceRequest struct {
	Cluster
	Name       string // logical service name (for logging)
	Image      string // target tag — used for both `docker build -t` and `docker push`
	Context    string // absolute or relative path to the build context
	Dockerfile string // path to the Dockerfile; default "<Context>/Dockerfile"
	Host       string // registry host (ghcr.io, docker.io, …) — must match <Image>'s host
	Username   string // resolved (post-$VAR-expansion)
	Password   string // resolved
	Runner     BuildRunner
}

// BuildService runs docker login → docker buildx build → docker push for
// a single service, using the operator's real `~/.docker/config.json`.
// Same contract as Kamal: the login auth entry stays in the user's
// docker config across deploys (idempotent; `docker login` overwrites
// the same host entry each time). That's the tradeoff for not breaking
// docker's plugin discovery and context resolution — previous attempts
// to override DOCKER_CONFIG for hygiene took the whole `docker buildx`
// subcommand with them.
func BuildService(ctx context.Context, req BuildServiceRequest) error {
	out := req.Log()
	runner := req.Runner
	if runner == nil {
		runner = DockerRunner{}
	}

	dockerfile := req.Dockerfile
	if dockerfile == "" {
		dockerfile = filepath.Join(req.Context, "Dockerfile")
	}
	if _, err := os.Stat(dockerfile); err != nil {
		return fmt.Errorf("services.%s.build: %s: %w", req.Name, dockerfile, err)
	}
	if _, err := os.Stat(req.Context); err != nil {
		return fmt.Errorf("services.%s.build: context %s: %w", req.Name, req.Context, err)
	}

	out.Command("build", req.Name, req.Image)

	out.Progress(fmt.Sprintf("docker login %s", req.Host))
	if err := runner.Login(ctx, req.Host, req.Username, req.Password); err != nil {
		return err
	}

	out.Progress(fmt.Sprintf("docker buildx build %s (context: %s)", req.Image, req.Context))
	if err := runner.Build(ctx, req.Image, req.Context, dockerfile, out.Writer(), out.Writer()); err != nil {
		return err
	}

	out.Progress(fmt.Sprintf("docker push %s", req.Image))
	if err := runner.Push(ctx, req.Image, out.Writer(), out.Writer()); err != nil {
		return err
	}

	out.Success(fmt.Sprintf("built & pushed %s", req.Image))
	return nil
}
