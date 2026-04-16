package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/getnvoi/nvoi/pkg/infra"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// isLocalSource returns true for local paths (. or / prefix), false for remote repos.
func isLocalSource(source string) bool {
	return strings.HasPrefix(source, ".") || strings.HasPrefix(source, "/")
}

type BuildRunRequest struct {
	Cluster
	Output             Output
	Builder            string
	BuilderCredentials map[string]string
	Source             string
	Dockerfile         string // path to Dockerfile relative to source (default: "Dockerfile")
	Name               string
	Branch             string
	Platform           string
	GitUsername        string // resolved by cmd layer (signed URL, gh, flag, env)
	GitToken           string
	History            int // keep N most recent tags, delete the rest (0 = keep all)
}

func BuildRun(ctx context.Context, req BuildRunRequest) (*provider.BuildResult, error) {
	// Validate source + builder combination
	local := isLocalSource(req.Source)
	if local && req.Builder == "daytona" {
		return nil, ErrInputf("local source %q cannot use --builder daytona — Daytona needs a git repo", req.Source)
	}
	if !local && req.Builder == "local" {
		return nil, ErrInputf("remote source %q cannot use --builder local — local builder can't clone remote repos", req.Source)
	}

	// Resolve git auth: signed URL overrides cmd-layer token
	source := req.Source
	gitUsername, gitToken := req.GitUsername, req.GitToken
	if cleanURL, user, token, ok := parseSignedURL(source); ok {
		source = cleanURL
		gitUsername = user
		gitToken = token
	}
	if gitToken != "" && gitUsername == "" {
		gitUsername = "x-access-token"
	}

	// Daytona + GitHub builders require git auth for remote sources
	if (req.Builder == "daytona" || req.Builder == "github") && gitToken == "" {
		return nil, ErrInput("git authentication required for remote source.\n  Use a signed URL:  --source https://user:TOKEN@github.com/org/repo\n  Or install gh CLI:  gh auth login\n  Or pass explicitly: --git-token TOKEN\n  Or set env var:     export GITHUB_TOKEN=...")
	}

	if req.MasterIP == "" {
		return nil, ErrInput("MasterIP is required for build")
	}

	builder, err := provider.ResolveBuild(req.Builder, req.BuilderCredentials)
	if err != nil {
		return nil, err
	}

	// Auto-detect platform from master architecture
	platform := req.Platform
	if platform == "" {
		platform = detectPlatform(ctx, req.MasterIP, req.SSHKey)
	}

	// Validate architecture per builder
	if req.Builder == "daytona" && platform == "linux/arm64" {
		return nil, ErrInput("--architecture arm64 is not available with --builder daytona — Daytona sandboxes are amd64 only")
	}

	out := log(req.Output)
	out.Command("build", "run", req.Name, "builder", req.Builder, "source", source)

	result, err := builder.Build(ctx, provider.BuildRequest{
		ServiceName: req.Name,
		Source:      source,
		Dockerfile:  req.Dockerfile,
		Branch:      req.Branch,
		Platform:    platform,
		GitUsername: gitUsername,
		GitToken:    gitToken,
		RegistrySSH: provider.SSHAccess{
			MasterIP:        req.MasterIP,
			MasterPrivateIP: req.MasterPrivateIP,
			PrivKey:         req.SSHKey,
		},
		Stdout: out.Writer(),
		Stderr: out.Writer(),
	})
	if err != nil {
		return nil, err
	}

	out.Success(result.ImageRef)

	// Prune old tags if --history is set
	if req.History > 0 {
		if err := pruneRegistryTags(ctx, req.Output, req.MasterIP, req.MasterPrivateIP, req.SSHKey, req.Name, req.History); err != nil {
			out.Warning(fmt.Sprintf("failed to prune old tags: %v", err))
		}
	}

	return result, nil
}

// ── Parallel builds ──────────────────────────────────────────────────────────

// BuildTarget is a name:source pair for parallel builds.
type BuildTarget struct {
	Name   string
	Source string
}

// ParseBuildTargets parses "name:source" args.
// Source can be a local path or remote URL. The first ":" that isn't
// part of a URL scheme (e.g. https://) is the delimiter.
func ParseBuildTargets(args []string) ([]BuildTarget, error) {
	var targets []BuildTarget
	for _, arg := range args {
		name, source, err := splitTarget(arg)
		if err != nil {
			return nil, err
		}
		targets = append(targets, BuildTarget{Name: name, Source: source})
	}
	return targets, nil
}

func splitTarget(arg string) (name, source string, err error) {
	idx := strings.Index(arg, ":")
	if idx < 0 {
		return "", "", ErrInputf("invalid target %q — expected name:source (e.g. web:./cmd/web)", arg)
	}
	// Check if the colon is part of a URL scheme (e.g. "web:https://...")
	// If what follows the colon starts with "//", this is the real delimiter.
	// If not, and there's another colon later (like in https://), the first colon is still the delimiter
	// because the name can't contain "https" as a valid image name.
	name = arg[:idx]
	source = arg[idx+1:]
	if name == "" || source == "" {
		return "", "", ErrInputf("invalid target %q — expected name:source (e.g. web:./cmd/web)", arg)
	}
	return name, source, nil
}

// BuildParallelRequest builds multiple images concurrently.
type BuildParallelRequest struct {
	Cluster
	Output             Output
	Builder            string
	BuilderCredentials map[string]string
	Targets            []BuildTarget
	Platform           string
	GitUsername        string
	GitToken           string
}

// BuildParallel runs builds concurrently, one goroutine per target.
func BuildParallel(ctx context.Context, req BuildParallelRequest) error {
	out := log(req.Output)

	if req.MasterIP == "" {
		return ErrInput("MasterIP is required for build")
	}

	builder, err := provider.ResolveBuild(req.Builder, req.BuilderCredentials)
	if err != nil {
		return err
	}

	// Auto-detect platform from master
	platform := req.Platform
	if platform == "" {
		platform = detectPlatform(ctx, req.MasterIP, req.SSHKey)
	}

	// Resolve git auth
	gitUsername, gitToken := req.GitUsername, req.GitToken
	if gitToken != "" && gitUsername == "" {
		gitUsername = "x-access-token"
	}

	// Validate architecture per builder (before spawning goroutines)
	if req.Builder == "daytona" && platform == "linux/arm64" {
		return ErrInput("--architecture arm64 is not available with --builder daytona — Daytona sandboxes are amd64 only")
	}

	// Pre-validate all targets before spawning goroutines
	for _, t := range req.Targets {
		local := isLocalSource(t.Source)
		if local && req.Builder == "daytona" {
			return ErrInputf("target %s: local source %q cannot use --builder daytona — Daytona needs a git repo", t.Name, t.Source)
		}
		if !local && req.Builder == "local" {
			return ErrInputf("target %s: remote source %q cannot use --builder local — local builder can't clone remote repos", t.Name, t.Source)
		}
	}

	// Check git auth for remote builders (after signed URL extraction could supply tokens)
	if req.Builder == "daytona" || req.Builder == "github" {
		hasAnyRemote := false
		for _, t := range req.Targets {
			if !isLocalSource(t.Source) {
				// Check if source has embedded credentials
				if _, _, token, ok := parseSignedURL(t.Source); ok && token != "" {
					continue
				}
				hasAnyRemote = true
			}
		}
		if hasAnyRemote && gitToken == "" {
			return ErrInput("git authentication required for remote source.\n  Use a signed URL:  --target name:https://user:TOKEN@github.com/org/repo\n  Or install gh CLI:  gh auth login\n  Or pass explicitly: --git-token TOKEN\n  Or set env var:     export GITHUB_TOKEN=...")
		}
	}

	out.Command("build", "parallel", fmt.Sprintf("%d targets", len(req.Targets)))

	g, ctx := errgroup.WithContext(ctx)
	for _, target := range req.Targets {
		t := target
		g.Go(func() error {
			pw := &prefixWriter{prefix: fmt.Sprintf("[%s] ", t.Name), w: out.Writer()}
			defer pw.Flush()

			// Resolve signed URLs per target (credentials may be embedded in the source URL)
			source := t.Source
			targetUsername, targetToken := gitUsername, gitToken
			if cleanURL, user, token, ok := parseSignedURL(source); ok {
				source = cleanURL
				targetUsername = user
				targetToken = token
			}
			if targetToken != "" && targetUsername == "" {
				targetUsername = "x-access-token"
			}

			fmt.Fprintf(pw, "building %s from %s (platform: %s)\n", t.Name, source, platform)

			result, err := builder.Build(ctx, provider.BuildRequest{
				ServiceName: t.Name,
				Source:      source,
				Platform:    platform,
				GitUsername: targetUsername,
				GitToken:    targetToken,
				RegistrySSH: provider.SSHAccess{
					MasterIP:        req.MasterIP,
					MasterPrivateIP: req.MasterPrivateIP,
					PrivKey:         req.SSHKey,
				},
				Stdout: pw,
				Stderr: pw,
			})
			if err != nil {
				return fmt.Errorf("%s: %w", t.Name, err)
			}

			out.Success(fmt.Sprintf("%s → %s", t.Name, result.ImageRef))
			return nil
		})
	}

	return g.Wait()
}

// prefixWriter prepends a prefix to each line written.
type prefixWriter struct {
	prefix string
	w      io.Writer
	mu     sync.Mutex
	buf    bytes.Buffer // partial line buffer
}

func (p *prefixWriter) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	n := len(data)
	p.buf.Write(data)

	for {
		line, err := p.buf.ReadString('\n')
		if err != nil {
			// No complete line yet — put it back
			p.buf.WriteString(line)
			break
		}
		if _, werr := fmt.Fprint(p.w, p.prefix+line); werr != nil {
			return 0, werr
		}
	}
	return n, nil
}

// Flush writes any remaining partial line in the buffer.
func (p *prefixWriter) Flush() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.buf.Len() > 0 {
		fmt.Fprintf(p.w, "%s%s\n", p.prefix, p.buf.String())
		p.buf.Reset()
	}
}

// detectPlatform auto-detects the target platform from the master node's architecture.
func detectPlatform(ctx context.Context, masterIP string, sshKey []byte) string {
	sshConn, connErr := infra.ConnectSSH(ctx, masterIP+":22", utils.DefaultUser, sshKey)
	if connErr != nil {
		return "linux/amd64"
	}
	out, runErr := sshConn.Run(ctx, "uname -m")
	sshConn.Close()
	if runErr != nil {
		return "linux/amd64"
	}
	arch := strings.TrimSpace(string(out))
	if arch == "aarch64" || arch == "arm64" {
		return "linux/arm64"
	}
	return "linux/amd64"
}

func pruneRegistryTags(ctx context.Context, o Output, masterIP, masterPrivateIP string, sshKey []byte, imageName string, keep int) error {
	plog := log(o)
	ssh, err := infra.ConnectSSH(ctx, masterIP+":22", utils.DefaultUser, sshKey)
	if err != nil {
		return err
	}
	defer ssh.Close()

	registryAddr := utils.RegistryAddr(masterPrivateIP)

	out, err := ssh.Run(ctx, fmt.Sprintf("curl -sf http://%s/v2/%s/tags/list", registryAddr, imageName))
	if err != nil {
		return err
	}

	var tagList struct {
		Tags []string `json:"tags"`
	}
	if err := json.Unmarshal(out, &tagList); err != nil {
		return err
	}

	if len(tagList.Tags) <= keep {
		return nil
	}

	// Tags are timestamp-based — sort ascending, delete the oldest
	sort.Strings(tagList.Tags)
	toDelete := tagList.Tags[:len(tagList.Tags)-keep]

	for _, tag := range toDelete {
		headCmd := fmt.Sprintf(
			"curl -sI -H 'Accept: application/vnd.oci.image.index.v1+json' http://%s/v2/%s/manifests/%s",
			registryAddr, imageName, tag,
		)
		headOut, err := ssh.Run(ctx, headCmd)
		if err != nil {
			continue
		}
		digest := ""
		for _, line := range strings.Split(string(headOut), "\n") {
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "docker-content-digest:") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					digest = strings.TrimSpace(parts[1])
				}
				break
			}
		}
		if digest == "" {
			continue
		}

		deleteCmd := fmt.Sprintf("curl -s -X DELETE http://%s/v2/%s/manifests/%s", registryAddr, imageName, digest)
		if _, err := ssh.Run(ctx, deleteCmd); err != nil {
			continue
		}
		plog.Info(fmt.Sprintf("pruned %s:%s", imageName, tag))
	}

	ssh.Run(ctx, "docker exec nvoi-registry bin/registry garbage-collect /etc/docker/registry/config.yml --delete-untagged 2>/dev/null")

	return nil
}

// parseSignedURL extracts credentials from URLs like https://user:token@github.com/org/repo.
func parseSignedURL(source string) (string, string, string, bool) {
	if !strings.Contains(source, "@") || !strings.HasPrefix(source, "http") {
		return source, "", "", false
	}
	u, err := url.Parse(source)
	if err != nil || u.User == nil {
		return source, "", "", false
	}
	password, hasPassword := u.User.Password()
	if !hasPassword {
		return source, "", "", false
	}
	username := u.User.Username()
	u.User = nil
	return u.String(), username, password, true
}

// ── Build list ────────────────────────────────────────────────────────────────

type RegistryImage struct {
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}

type BuildListRequest struct {
	Cluster
	Output      Output
	RunOnMaster infra.RunOnMaster
}

func BuildList(ctx context.Context, req BuildListRequest) ([]RegistryImage, error) {
	registryAddr := utils.RegistryAddr(req.MasterPrivateIP)

	out, err := req.RunOnMaster(ctx, fmt.Sprintf("curl -sf http://%s/v2/_catalog", registryAddr))
	if err != nil {
		return nil, fmt.Errorf("registry catalog: %w", err)
	}

	var catalog struct {
		Repositories []string `json:"repositories"`
	}
	if err := json.Unmarshal(out, &catalog); err != nil {
		return nil, fmt.Errorf("parse catalog: %w", err)
	}

	var images []RegistryImage
	for _, repo := range catalog.Repositories {
		tagOut, err := req.RunOnMaster(ctx, fmt.Sprintf("curl -sf http://%s/v2/%s/tags/list", registryAddr, repo))
		if err != nil {
			continue
		}
		var tagList struct {
			Tags []string `json:"tags"`
		}
		if err := json.Unmarshal(tagOut, &tagList); err != nil {
			continue
		}
		sort.Strings(tagList.Tags)
		images = append(images, RegistryImage{Name: repo, Tags: tagList.Tags})
	}

	return images, nil
}

// ── Build latest ──────────────────────────────────────────────────────────────

type BuildLatestRequest struct {
	Cluster
	Output      Output
	Name        string
	RunOnMaster infra.RunOnMaster
}

func BuildLatest(ctx context.Context, req BuildLatestRequest) (string, error) {
	registryAddr := utils.RegistryAddr(req.MasterPrivateIP)

	out, err := req.RunOnMaster(ctx, fmt.Sprintf("curl -sf http://%s/v2/%s/tags/list", registryAddr, req.Name))
	if err != nil {
		return "", ErrNotFound("image", req.Name)
	}

	var tagList struct {
		Tags []string `json:"tags"`
	}
	if err := json.Unmarshal(out, &tagList); err != nil {
		return "", fmt.Errorf("parse tags for %q: %w", req.Name, err)
	}
	if len(tagList.Tags) == 0 {
		return "", ErrNotFound("image tags", req.Name)
	}

	sort.Strings(tagList.Tags)
	latestTag := tagList.Tags[len(tagList.Tags)-1]

	return fmt.Sprintf("%s/%s:%s", registryAddr, req.Name, latestTag), nil
}
