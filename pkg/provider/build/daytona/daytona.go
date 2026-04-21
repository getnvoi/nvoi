// Package daytona implements pkg/provider.BuildProvider using Daytona
// managed sandboxes with Docker-in-Docker. Built as a port of the
// 9b39daf daytona provider to the new contract:
//
//   - Build signature is `(ctx, req) → (imageRef, error)` — matches every
//     other BuildProvider (local, ssh). Returns req.Image unchanged.
//   - Inner mechanics still boot a DinD sandbox, clone the source, run
//     `docker buildx build --push` inside the sandbox.
//   - What changed vs. 9b39daf: the SSH-tunnel-to-master-registry dance
//     is gone. The new model pushes to a real external registry (ghcr.io,
//     docker.io, ECR, …) whose credentials flow in via req.Registry, same
//     as ssh + local builders. Simpler, one less thing to go wrong, matches
//     the "each BuildProvider is self-contained" invariant.
//   - Source is req.GitRemote @ req.GitRef instead of Source/Branch. The
//     CLI infers both from the operator's cwd via `git remote get-url origin`
//     and `git rev-parse HEAD`.
//
// Credentials: daytona api_key (DAYTONA_API_KEY). Registry auth rides on
// req.Registry — the CLI resolved it before calling Build.
package daytona

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/getnvoi/nvoi/pkg/provider"
)

// sandboxHome is the home directory inside the nvoi-dind image. Every
// path we interact with lives under this (ssh/, src/, etc.).
const sandboxHome = "/home/daytona"

// Poll intervals for long-running operations. Declared as vars so tests
// can drop them to millisecond-scale without sleeping. Production values
// match 9b39daf. Mutating these in non-test code is a bug.
var (
	dockerPollInterval  = 2 * time.Second
	sessionPollInterval = 2 * time.Second
)

// DaytonaBuilder is the registered BuildProvider for name "daytona".
type DaytonaBuilder struct {
	apiKey    string
	newClient func(apiKey string) (client, error)
}

// New returns a DaytonaBuilder wired to the production Daytona SDK.
func New(apiKey string) *DaytonaBuilder {
	return &DaytonaBuilder{
		apiKey:    apiKey,
		newClient: newSDKClient,
	}
}

// Build boots (or resumes) a per-service DinD sandbox, clones the repo,
// runs `docker buildx build --push` inside, and returns req.Image on
// success. Sandbox is stopped on return (preserved for cache reuse on
// next deploy) — NOT deleted.
func (b *DaytonaBuilder) Build(ctx context.Context, req provider.BuildRequest) (string, error) {
	if err := validateBuildRequest(req); err != nil {
		return "", err
	}

	c, err := b.newClient(b.apiKey)
	if err != nil {
		return "", fmt.Errorf("daytona build: new client: %w", err)
	}

	out := streamWriter(req.Output)
	progress := func(msg string) { _, _ = out.Write([]byte(msg + "\n")) }

	sandboxName := "nvoi-build-" + sanitize(req.Service)
	progress(fmt.Sprintf("daytona build: acquire sandbox %s", sandboxName))
	sb, err := c.FindOrStartOrCreate(ctx, sandboxName)
	if err != nil {
		return "", fmt.Errorf("daytona build: sandbox acquire: %w", err)
	}
	// Stop (not delete) on return: preserves BuildKit cache + git history
	// for the next deploy. Daytona charges for started-time, not
	// stopped-time, so this is the cost-efficient default.
	defer func() {
		_ = sb.Stop(context.Background())
	}()

	// DinD takes a few seconds to come up after sandbox Start — poll the
	// docker daemon socket before any build command fires.
	progress("daytona build: waiting for docker daemon inside DinD sandbox")
	if err := waitForDocker(ctx, sb, 2*time.Minute); err != nil {
		return "", fmt.Errorf("daytona build: wait for docker: %w", err)
	}

	// Clone the repo at the exact ref. Daytona's Git.Clone takes a branch
	// name, not a SHA; we clone to HEAD of origin's default branch, then
	// `git fetch --depth=1 origin <sha> && git checkout FETCH_HEAD` so the
	// tree matches req.GitRef verbatim. Same SHA-pinning rationale as the
	// ssh builder.
	repoPath := sandboxHome + "/src"
	progress(fmt.Sprintf("daytona build: git clone %s @ %s → %s", req.GitRemote, shortSHA(req.GitRef), repoPath))
	if err := cloneAtCommit(ctx, sb, req.GitRemote, req.GitRef, repoPath); err != nil {
		return "", fmt.Errorf("daytona build: clone: %w", err)
	}

	// Docker login — same --password-stdin contract as ssh + local. Never
	// appears in argv on the sandbox shell history.
	if req.Registry.Username != "" || req.Registry.Password != "" {
		progress(fmt.Sprintf("daytona build: docker login %s", req.Registry.Host))
		if err := dockerLogin(ctx, sb, req.Registry); err != nil {
			return "", fmt.Errorf("daytona build: %w", err)
		}
	}

	// Build + push in one buildx invocation. `--output
	// type=image,push=true` is the BuildKit-native push (same as `--push`,
	// kept verbose for 9b39daf parity). Long-running; driven via the
	// split-session-exec dance to survive past the SDK's 60s HTTP timeout.
	buildCtx := joinRepoPath(repoPath, req.Context)
	dockerfile := resolveDockerfile(buildCtx, req.Dockerfile)
	buildCmd := fmt.Sprintf(
		"docker buildx build --output type=image,push=true --platform %s --tag %s --file %s %s",
		shellQuote(req.Platform),
		shellQuote(req.Image),
		shellQuote(dockerfile),
		shellQuote(buildCtx),
	)
	progress(fmt.Sprintf("daytona build: docker buildx build --push -t %s (platform %s)", req.Image, req.Platform))
	exitCode, buildErr := execAsync(ctx, sb, buildCmd, 15*time.Minute, func(line string) {
		_, _ = out.Write([]byte(line + "\n"))
	})
	if buildErr != nil {
		return "", fmt.Errorf("daytona build: %w", buildErr)
	}
	if exitCode != 0 {
		return "", fmt.Errorf("daytona build: docker buildx exited %d", exitCode)
	}

	return req.Image, nil
}

// Close is a no-op; DaytonaBuilder holds no long-lived resources (each
// Build call acquires its own SDK client and releases it implicitly when
// the sandbox is stopped).
func (*DaytonaBuilder) Close() error { return nil }

// ── helpers ──────────────────────────────────────────────────────────────

// validateBuildRequest mirrors ssh.validateBuildRequest — identical
// required fields since both remote builders need the same inputs.
// RequiresBuilders is false for daytona (it uses the sandbox substrate,
// not role:builder servers), so Builders and SSHKey are NOT required.
func validateBuildRequest(req provider.BuildRequest) error {
	if req.Image == "" {
		return fmt.Errorf("services.%s.build: Image is required", req.Service)
	}
	if req.Platform == "" {
		return fmt.Errorf("services.%s.build: Platform is required — an empty platform would silently build for the sandbox's host arch", req.Service)
	}
	if req.GitRemote == "" {
		return fmt.Errorf("services.%s.build: GitRemote is empty — the operator's cwd must be a git checkout; the CLI should have inferred it via `git remote get-url origin`", req.Service)
	}
	if req.GitRef == "" {
		return fmt.Errorf("services.%s.build: GitRef is empty — the CLI should have pinned HEAD via `git rev-parse HEAD`", req.Service)
	}
	if req.Service == "" {
		return fmt.Errorf("daytona build: Service name is required (used as sandbox key for cache reuse)")
	}
	return nil
}

// cloneAtCommit clones the repo via the Daytona Git API (which clones to a
// branch at HEAD), then inside the sandbox fetches + checks out the exact
// SHA. One session call for the SHA checkout — fast, and reuses the git
// config Daytona's Clone() wrote.
func cloneAtCommit(ctx context.Context, sb sandbox, remote, sha, path string) error {
	// Clear any stale clone from a prior run (e.g. cancelled deploy).
	if _, _, err := sb.Exec(ctx, "rm -rf "+shellQuote(path), time.Minute); err != nil {
		return fmt.Errorf("prepare repo path: %w", err)
	}
	// Daytona's Clone() wraps `git clone` — it takes a branch, not a SHA.
	// We clone the default branch shallow, then fetch the SHA we actually
	// want. Branch "" means HEAD of origin's default.
	if err := sb.Clone(ctx, remote, path, "", "", ""); err != nil {
		return fmt.Errorf("git clone %s: %w", remote, err)
	}
	// Now pin to the exact SHA. Reuses the repo config + creds Daytona
	// set up for the clone.
	fetchCmd := fmt.Sprintf(
		"cd %s && git fetch --depth=1 origin %s && git checkout FETCH_HEAD",
		shellQuote(path), shellQuote(sha),
	)
	out, code, err := sb.Exec(ctx, fetchCmd, 2*time.Minute)
	if err != nil {
		return fmt.Errorf("git fetch %s: %w", shortSHA(sha), err)
	}
	if code != 0 {
		return fmt.Errorf("git fetch %s exit %d: %s", shortSHA(sha), code, strings.TrimSpace(out))
	}
	return nil
}

// dockerLogin pipes the password via a heredoc-less stdin trick
// (`printf '%s' PASSWORD | docker login --password-stdin`). Using the
// sandbox's Exec rather than streaming because `docker login` is fast
// (sub-second) and the Exec API handles the single round-trip cleanly.
// Password is shell-quoted — `'` inside the password is escaped via the
// close-reopen trick.
func dockerLogin(ctx context.Context, sb sandbox, auth provider.RegistryAuth) error {
	cmd := fmt.Sprintf(
		"printf '%%s' %s | docker login %s -u %s --password-stdin",
		shellQuote(auth.Password),
		shellQuote(auth.Host),
		shellQuote(auth.Username),
	)
	out, code, err := sb.Exec(ctx, cmd, 2*time.Minute)
	if err != nil {
		return fmt.Errorf("docker login %s: %w", auth.Host, err)
	}
	if code != 0 {
		return fmt.Errorf("docker login %s exit %d: %s", auth.Host, code, strings.TrimSpace(out))
	}
	return nil
}

// waitForDocker polls `docker info` inside the sandbox every 2s until the
// DinD daemon answers. Identical semantics to 9b39daf.
func waitForDocker(ctx context.Context, sb sandbox, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(dockerPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("timed out waiting for docker")
		case <-ticker.C:
			out, code, err := sb.Exec(ctx, "docker info >/dev/null 2>&1 && echo ready", 30*time.Second)
			if err == nil && code == 0 && strings.Contains(out, "ready") {
				return nil
			}
		}
	}
}

// execAsync runs a long-running command in a session to side-step the
// Daytona SDK's 60-second HTTP client timeout. Verbatim from 9b39daf —
// see the commit message for the rationale on split-session-exec.
func execAsync(ctx context.Context, sb sandbox, command string, timeout time.Duration, onLine func(string)) (int, error) {
	sessionID := fmt.Sprintf("build-%d", time.Now().UnixNano())
	if err := sb.CreateSession(ctx, sessionID); err != nil {
		return 0, fmt.Errorf("create session: %w", err)
	}
	defer sb.DeleteSession(ctx, sessionID)

	cmdID, err := sb.ExecSessionAsync(ctx, sessionID, command)
	if err != nil {
		return 0, fmt.Errorf("exec session command: %w", err)
	}

	stdout := make(chan string, 100)
	stderr := make(chan string, 100)
	go sb.StreamSessionLogs(ctx, sessionID, cmdID, stdout, stderr)

	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case line, ok := <-stdout:
				if !ok {
					stdout = nil
					if stderr == nil {
						return
					}
					continue
				}
				logChunk(&buf, line, onLine)
			case line, ok := <-stderr:
				if !ok {
					stderr = nil
					if stdout == nil {
						return
					}
					continue
				}
				logChunk(&buf, line, onLine)
			}
		}
	}()

	deadlineTimer := time.NewTimer(timeout)
	defer deadlineTimer.Stop()
	ticker := time.NewTicker(sessionPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-deadlineTimer.C:
			return 0, fmt.Errorf("build timed out after %s", timeout)
		case <-ticker.C:
			exitCode, finished, err := sb.SessionCommand(ctx, sessionID, cmdID)
			if err != nil {
				continue
			}
			if finished {
				<-done
				if buf.Len() > 0 {
					onLine(buf.String())
				}
				return exitCode, nil
			}
		}
	}
}

// logChunk buffers partial lines and emits complete ones. Verbatim from
// 9b39daf — Daytona's session log stream arrives in arbitrary-sized chunks
// so we split on '\n' ourselves.
func logChunk(buf *bytes.Buffer, chunk string, emit func(string)) {
	buf.WriteString(strings.ReplaceAll(chunk, "\r\n", "\n"))
	for {
		data := buf.String()
		idx := strings.IndexByte(data, '\n')
		if idx < 0 {
			return
		}
		line := data[:idx]
		emit(line)
		buf.Reset()
		buf.WriteString(data[idx+1:])
	}
}

// joinRepoPath resolves repo-relative Context against the clone dir. Empty
// or "." means "build the repo root". Matches the ssh builder's helper —
// same repo-relative contract.
func joinRepoPath(repoPath, ctxPath string) string {
	ctxPath = strings.TrimPrefix(ctxPath, "./")
	ctxPath = strings.Trim(ctxPath, "/")
	if ctxPath == "" || ctxPath == "." {
		return repoPath
	}
	return repoPath + "/" + ctxPath
}

// resolveDockerfile mirrors ssh.resolveDockerfile — same rules (empty →
// <ctx>/Dockerfile, absolute → verbatim, else ctx-relative).
func resolveDockerfile(buildCtx, dockerfile string) string {
	if dockerfile == "" {
		return buildCtx + "/Dockerfile"
	}
	if strings.HasPrefix(dockerfile, "/") {
		return dockerfile
	}
	return buildCtx + "/" + dockerfile
}

// shellQuote wraps s in single quotes and escapes embedded single quotes.
// Same helper shape as ssh and 9b39daf.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// sanitize maps a service name to a sandbox-safe name fragment — daytona
// caps at 50 chars.
func sanitize(s string) string {
	s = strings.ReplaceAll(s, "/", "-")
	s = strings.ReplaceAll(s, ":", "-")
	if len(s) > 50 {
		s = s[:50]
	}
	return s
}

// shortSHA is a 7-char prefix for log readability.
func shortSHA(ref string) string {
	if len(ref) >= 7 {
		return ref[:7]
	}
	return ref
}

// streamWriter normalizes req.Output — nil falls back to io.Discard so
// Build never nil-derefs.
func streamWriter(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}
