package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/getnvoi/nvoi/internal/core"
	"github.com/getnvoi/nvoi/internal/infra"
	"github.com/getnvoi/nvoi/internal/provider"
)

// isLocalSource returns true for local paths (. or / prefix), false for remote repos.
func isLocalSource(source string) bool {
	return strings.HasPrefix(source, ".") || strings.HasPrefix(source, "/")
}

type BuildRunRequest struct {
	AppName            string
	Env                string
	Provider           string
	Credentials        map[string]string
	Builder            string
	BuilderCredentials map[string]string
	SSHKey             []byte
	Source             string
	Name               string
	Branch             string
	Platform           string
	GitUsername         string // resolved by cmd layer (signed URL, gh, flag, env)
	GitToken           string
}

func BuildRun(ctx context.Context, req BuildRunRequest) (*provider.BuildResult, error) {
	// Validate source + builder combination
	local := isLocalSource(req.Source)
	if local && req.Builder == "daytona" {
		return nil, fmt.Errorf("local source %q cannot use --builder daytona — Daytona needs a git repo", req.Source)
	}
	if !local && req.Builder == "local" {
		return nil, fmt.Errorf("remote source %q cannot use --builder local — local builder can't clone remote repos", req.Source)
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

	// Daytona builder requires git auth for remote sources
	if req.Builder == "daytona" && gitToken == "" {
		return nil, fmt.Errorf("git authentication required for remote source.\n  Use a signed URL:  --source https://user:TOKEN@github.com/org/repo\n  Or install gh CLI:  gh auth login\n  Or pass explicitly: --git-token TOKEN\n  Or set env var:     export GITHUB_TOKEN=...")
	}

	names, err := core.NewNames(req.AppName, req.Env)
	if err != nil {
		return nil, err
	}
	prov, err := provider.ResolveCompute(req.Provider, req.Credentials)
	if err != nil {
		return nil, err
	}
	master, err := FindMaster(ctx, prov, names)
	if err != nil {
		return nil, err
	}

	builder, err := provider.ResolveBuild(req.Builder, req.BuilderCredentials)
	if err != nil {
		return nil, err
	}

	// Auto-detect platform from master architecture
	platform := req.Platform
	if platform == "" {
		sshConn, connErr := infra.ConnectSSH(ctx, master.IPv4+":22", core.DefaultUser, req.SSHKey)
		if connErr == nil {
			out, runErr := sshConn.Run(ctx, "uname -m")
			sshConn.Close()
			if runErr == nil {
				arch := strings.TrimSpace(string(out))
				if arch == "aarch64" || arch == "arm64" {
					platform = "linux/arm64"
				} else {
					platform = "linux/amd64"
				}
			}
		}
		if platform == "" {
			platform = "linux/amd64"
		}
	}

	fmt.Printf("==> build %s (builder: %s, source: %s)\n", req.Name, req.Builder, source)

	result, err := builder.Build(ctx, provider.BuildRequest{
		ServiceName: req.Name,
		Source:      source,
		Branch:      req.Branch,
		Platform:    platform,
		GitUsername:  gitUsername,
		GitToken:    gitToken,
		RegistrySSH: provider.SSHAccess{
			MasterIP:        master.IPv4,
			MasterPrivateIP: master.PrivateIP,
			PrivKey:         req.SSHKey,
		},
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	})
	if err != nil {
		return nil, err
	}

	fmt.Printf("  ✓ %s\n", result.ImageRef)
	return result, nil
}

// parseSignedURL extracts credentials from URLs like https://user:token@github.com/org/repo.
// Returns (cleanURL, username, token, ok).
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
	u.User = nil // strip credentials
	return u.String(), username, password, true
}

// ── Build list ────────────────────────────────────────────────────────────────

type RegistryImage struct {
	Name string
	Tags []string
}

type BuildListRequest struct {
	AppName     string
	Env         string
	Provider    string
	Credentials map[string]string
	SSHKey      []byte
}

func BuildList(ctx context.Context, req BuildListRequest) ([]RegistryImage, error) {
	names, err := core.NewNames(req.AppName, req.Env)
	if err != nil {
		return nil, err
	}
	prov, err := provider.ResolveCompute(req.Provider, req.Credentials)
	if err != nil {
		return nil, err
	}
	master, err := FindMaster(ctx, prov, names)
	if err != nil {
		return nil, err
	}

	ssh, err := infra.ConnectSSH(ctx, master.IPv4+":22", core.DefaultUser, req.SSHKey)
	if err != nil {
		return nil, fmt.Errorf("ssh master: %w", err)
	}
	defer ssh.Close()

	registryAddr := core.RegistryAddr(master.PrivateIP)

	out, err := ssh.Run(ctx, fmt.Sprintf("curl -sf http://%s/v2/_catalog", registryAddr))
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
		tagOut, err := ssh.Run(ctx, fmt.Sprintf("curl -sf http://%s/v2/%s/tags/list", registryAddr, repo))
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
	AppName     string
	Env         string
	Provider    string
	Credentials map[string]string
	SSHKey      []byte
	Name        string
}

func BuildLatest(ctx context.Context, req BuildLatestRequest) (string, error) {
	names, err := core.NewNames(req.AppName, req.Env)
	if err != nil {
		return "", err
	}
	prov, err := provider.ResolveCompute(req.Provider, req.Credentials)
	if err != nil {
		return "", err
	}
	master, err := FindMaster(ctx, prov, names)
	if err != nil {
		return "", err
	}

	ssh, err := infra.ConnectSSH(ctx, master.IPv4+":22", core.DefaultUser, req.SSHKey)
	if err != nil {
		return "", fmt.Errorf("ssh master: %w", err)
	}
	defer ssh.Close()

	registryAddr := core.RegistryAddr(master.PrivateIP)

	out, err := ssh.Run(ctx, fmt.Sprintf("curl -sf http://%s/v2/%s/tags/list", registryAddr, req.Name))
	if err != nil {
		return "", fmt.Errorf("image %q not found in registry", req.Name)
	}

	var tagList struct {
		Tags []string `json:"tags"`
	}
	if err := json.Unmarshal(out, &tagList); err != nil {
		return "", fmt.Errorf("parse tags for %q: %w", req.Name, err)
	}
	if len(tagList.Tags) == 0 {
		return "", fmt.Errorf("no tags found for %q", req.Name)
	}

	// Tags are timestamp-based (20060102-150405), lexicographic sort = chronological order
	sort.Strings(tagList.Tags)
	latestTag := tagList.Tags[len(tagList.Tags)-1]

	return fmt.Sprintf("%s/%s:%s", registryAddr, req.Name, latestTag), nil
}
