// Package daytona implements provider.BuildProvider using Daytona remote sandbox builds.
//
// Spins up a sandbox with Docker-in-Docker, clones the repo, sets up an SSH tunnel
// to the private registry on the master server, and runs docker buildx build --push.
package daytona

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/getnvoi/nvoi/pkg/utils"
	"github.com/getnvoi/nvoi/pkg/provider"
)

// Builder builds images in a Daytona sandbox and pushes through an SSH tunnel.
type Builder struct {
	apiKey    string
	newClient func(apiKey string) (client, error)
}

// NewBuilder creates a Daytona builder with explicit API key.
func NewBuilder(apiKey string) *Builder {
	return &Builder{
		apiKey:    apiKey,
		newClient: newSDKClient,
	}
}

func (b *Builder) Build(ctx context.Context, req provider.BuildRequest) (*provider.BuildResult, error) {
	const sandboxHome = "/home/daytona"

	c, err := b.newClient(b.apiKey)
	if err != nil {
		return nil, err
	}

	sandboxName := "nvoi-build-" + sanitize(req.ServiceName)
	sb, err := c.FindOrStartOrCreate(ctx, sandboxName)
	if err != nil {
		return nil, err
	}

	// Wait for Docker daemon inside DinD sandbox
	fmt.Fprintf(req.Stdout, "  waiting for docker in sandbox...\n")
	if err := waitForDocker(ctx, sb, 2*time.Minute); err != nil {
		sb.Stop(context.Background())
		return nil, fmt.Errorf("wait for docker: %w", err)
	}

	// Upload SSH key for registry tunnel
	if len(req.RegistrySSH.PrivKey) > 0 {
		out, code, err := sb.Exec(ctx, "mkdir -p "+sandboxHome+"/.ssh && chmod 700 "+sandboxHome+"/.ssh", time.Minute)
		if err != nil || code != 0 {
			sb.Stop(context.Background())
			return nil, commandError("prepare ssh dir", out, code, err)
		}
		keyPath := sandboxHome + "/.ssh/id_rsa"
		if err := sb.Upload(ctx, req.RegistrySSH.PrivKey, keyPath); err != nil {
			sb.Stop(context.Background())
			return nil, fmt.Errorf("upload ssh key: %w", err)
		}
		out, code, err = sb.Exec(ctx, fmt.Sprintf("chmod 600 %s", shellQuote(keyPath)), time.Minute)
		if err != nil || code != 0 {
			sb.Stop(context.Background())
			return nil, commandError("chmod ssh key", out, code, err)
		}
		sshConfig := fmt.Sprintf("Host *\n  StrictHostKeyChecking no\n  UserKnownHostsFile /dev/null\n  IdentityFile %s\n", keyPath)
		configPath := sandboxHome + "/.ssh/config"
		if err := sb.Upload(ctx, []byte(sshConfig), configPath); err != nil {
			sb.Stop(context.Background())
			return nil, fmt.Errorf("upload ssh config: %w", err)
		}
		out, code, err = sb.Exec(ctx, fmt.Sprintf("chmod 600 %s", shellQuote(configPath)), time.Minute)
		if err != nil || code != 0 {
			sb.Stop(context.Background())
			return nil, commandError("chmod ssh config", out, code, err)
		}
		fmt.Fprintf(req.Stdout, "  uploaded SSH key for registry tunnel\n")
	}

	// Clone repo
	repoURL := normalizeRepoURL(req.Source)
	branch := req.Branch
	if branch == "" {
		branch = "main"
	}
	repoPath := sandboxHome + "/src"
	out, code, err := sb.Exec(ctx, fmt.Sprintf("rm -rf %s", shellQuote(repoPath)), time.Minute)
	if err != nil || code != 0 {
		sb.Stop(context.Background())
		return nil, commandError("prepare repo path", out, code, err)
	}
	if err := sb.Clone(ctx, repoURL, repoPath, branch, req.GitUsername, req.GitToken); err != nil {
		sb.Stop(context.Background())
		return nil, fmt.Errorf("clone repo: %w", err)
	}
	fmt.Fprintf(req.Stdout, "  cloned %s → %s\n", repoURL, repoPath)

	// Start SSH tunnel from sandbox to registry on master
	registryAddr := utils.RegistryAddr(req.RegistrySSH.MasterPrivateIP)
	tunnelCmd := fmt.Sprintf(
		"ssh -N -f -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -i %s/.ssh/id_rsa -L %d:%s %s@%s",
		sandboxHome, utils.RegistryPort, registryAddr, utils.DefaultUser, req.RegistrySSH.MasterIP,
	)
	out, code, err = sb.Exec(ctx, tunnelCmd, 30*time.Second)
	if err != nil || code != 0 {
		sb.Stop(context.Background())
		return nil, commandError("start registry tunnel", out, code, err)
	}
	fmt.Fprintf(req.Stdout, "  SSH tunnel to registry established\n")

	// Build and push
	tag := time.Now().UTC().Format("20060102-150405")
	tunnelTag := fmt.Sprintf("localhost:%d/%s:%s", utils.RegistryPort, req.ServiceName, tag)
	registryRef := fmt.Sprintf("%s/%s:%s", registryAddr, req.ServiceName, tag)

	fmt.Fprintf(req.Stdout, "  building %s (platform: %s)...\n", req.ServiceName, req.Platform)

	buildCmd := fmt.Sprintf(
		"docker buildx build --tag %s --output type=image,push=true,registry.insecure=true",
		tunnelTag,
	)
	if req.Platform != "" {
		buildCmd += " --platform " + req.Platform
	}
	buildCmd += " " + repoPath

	exitCode, buildErr := execAsync(ctx, sb, buildCmd, 15*time.Minute, func(line string) {
		fmt.Fprintf(req.Stdout, "  %s\n", line)
	})
	if buildErr != nil || exitCode != 0 {
		sb.Stop(context.Background())
		return nil, commandError(fmt.Sprintf("build %s", req.ServiceName), "", exitCode, buildErr)
	}

	// Stop sandbox (preserves state for reuse)
	sb.Stop(context.Background())

	fmt.Fprintf(req.Stdout, "  ✓ pushed %s\n", registryRef)
	return &provider.BuildResult{ImageRef: registryRef}, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// normalizeRepoURL converts short-form repo refs to full GitHub URLs.
func normalizeRepoURL(source string) string {
	if strings.HasPrefix(source, "https://") || strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "git@") {
		return source
	}
	// org/repo → https://github.com/org/repo.git
	return "https://github.com/" + source + ".git"
}

func waitForDocker(ctx context.Context, sb sandbox, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(2 * time.Second)
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

func commandError(step, out string, code int, err error) error {
	if err != nil {
		return fmt.Errorf("%s failed (exit=%d): %w", step, code, err)
	}
	if out != "" {
		return fmt.Errorf("%s failed (exit=%d): %s", step, code, out)
	}
	return fmt.Errorf("%s failed (exit=%d)", step, code)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func sanitize(s string) string {
	s = strings.ReplaceAll(s, "/", "-")
	s = strings.ReplaceAll(s, ":", "-")
	if len(s) > 50 {
		s = s[:50]
	}
	return s
}

// execAsync runs a command in a session to avoid the Daytona SDK's 60-second
// HTTP client timeout. Uses CreateSession + ExecSessionAsync + StreamSessionLogs
// + poll for completion.
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

	// Stream logs in background
	stdout := make(chan string, 100)
	stderr := make(chan string, 100)
	go sb.StreamSessionLogs(ctx, sessionID, cmdID, stdout, stderr)

	// Drain logs and emit complete lines
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

	// Poll for completion
	deadlineTimer := time.NewTimer(timeout)
	defer deadlineTimer.Stop()
	ticker := time.NewTicker(2 * time.Second)
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
				<-done // wait for log drain
				if buf.Len() > 0 {
					onLine(buf.String())
				}
				return exitCode, nil
			}
		}
	}
}

// logChunk buffers partial lines and emits complete ones.
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
