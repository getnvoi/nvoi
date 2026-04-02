package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/infra"
	"github.com/getnvoi/nvoi/pkg/provider"
)

// isLocalSource returns true for local paths (. or / prefix), false for remote repos.
func isLocalSource(source string) bool {
	return strings.HasPrefix(source, ".") || strings.HasPrefix(source, "/")
}

type BuildRunRequest struct {
	Cluster
	Builder            string
	BuilderCredentials map[string]string
	Source             string
	Name               string
	Branch             string
	Platform           string
	GitUsername         string // resolved by cmd layer (signed URL, gh, flag, env)
	GitToken           string
	History            int // keep N most recent tags, delete the rest (0 = keep all)
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

	// Daytona + GitHub builders require git auth for remote sources
	if (req.Builder == "daytona" || req.Builder == "github") && gitToken == "" {
		return nil, fmt.Errorf("git authentication required for remote source.\n  Use a signed URL:  --source https://user:TOKEN@github.com/org/repo\n  Or install gh CLI:  gh auth login\n  Or pass explicitly: --git-token TOKEN\n  Or set env var:     export GITHUB_TOKEN=...")
	}

	master, _, _, err := req.Cluster.Master(ctx)
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

	// Validate architecture per builder
	if req.Builder == "daytona" && platform == "linux/arm64" {
		return nil, fmt.Errorf("--architecture arm64 is not available with --builder daytona — Daytona sandboxes are amd64 only")
	}

	out := req.Log()
	out.Command("build", "run", req.Name, "builder", req.Builder, "source", source)

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
		Stdout: out.Writer(),
		Stderr: out.Writer(),
	})
	if err != nil {
		return nil, err
	}

	out.Success(result.ImageRef)

	// Prune old tags if --history is set
	if req.History > 0 {
		if err := pruneRegistryTags(ctx, req.Cluster, master, req.Name, req.History); err != nil {
			out.Warning(fmt.Sprintf("failed to prune old tags: %v", err))
		}
	}

	return result, nil
}

func pruneRegistryTags(ctx context.Context, c Cluster, master *provider.Server, imageName string, keep int) error {
	log := c.Log()
	ssh, err := infra.ConnectSSH(ctx, master.IPv4+":22", core.DefaultUser, c.SSHKey)
	if err != nil {
		return err
	}
	defer ssh.Close()

	registryAddr := core.RegistryAddr(master.PrivateIP)

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
		log.Info(fmt.Sprintf("pruned %s:%s", imageName, tag))
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

// ── Build prune ───────────────────────────────────────────────────────────────

type BuildPruneRequest struct {
	Cluster
	Name string
	Keep int
}

func BuildPrune(ctx context.Context, req BuildPruneRequest) error {
	master, _, _, err := req.Cluster.Master(ctx)
	if err != nil {
		return err
	}
	return pruneRegistryTags(ctx, req.Cluster, master, req.Name, req.Keep)
}

// ── Build list ────────────────────────────────────────────────────────────────

type RegistryImage struct {
	Name string
	Tags []string
}

type BuildListRequest struct {
	Cluster
}

func BuildList(ctx context.Context, req BuildListRequest) ([]RegistryImage, error) {
	ssh, _, err := req.Cluster.SSH(ctx)
	if err != nil {
		return nil, err
	}
	defer ssh.Close()

	master, _, _, err := req.Cluster.Master(ctx)
	if err != nil {
		return nil, err
	}
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
	Cluster
	Name string
}

func BuildLatest(ctx context.Context, req BuildLatestRequest) (string, error) {
	ssh, _, err := req.Cluster.SSH(ctx)
	if err != nil {
		return "", err
	}
	defer ssh.Close()

	master, _, _, err := req.Cluster.Master(ctx)
	if err != nil {
		return "", err
	}
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

	sort.Strings(tagList.Tags)
	latestTag := tagList.Tags[len(tagList.Tags)-1]

	return fmt.Sprintf("%s/%s:%s", registryAddr, req.Name, latestTag), nil
}
