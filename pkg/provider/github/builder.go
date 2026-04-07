// Package github implements provider.BuildProvider using GitHub Actions.
//
// Manages the workflow file in the repo via Contents API, sets the SSH key
// as a repo secret via Secrets API, triggers workflow_dispatch, and polls
// the Actions API for completion.
package github

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

const workflowPath = ".github/workflows/nvoi-build.yml"

// workflowYAML is the self-contained GitHub Actions workflow.
// nvoi commits this to the repo — the user never writes it.
const workflowYAML = `name: nvoi-build
run-name: "nvoi build ${{ inputs.name }}:${{ inputs.tag }}"
on:
  workflow_dispatch:
    inputs:
      name:
        description: Image name
        required: true
      registry:
        description: Registry address (privateIP:port)
        required: true
      master_ip:
        description: Master server public IP
        required: true
      platform:
        description: Build platform
        required: true
      tag:
        description: Image tag
        required: true
      runner:
        description: GitHub Actions runner
        required: true
        default: ubuntu-latest

jobs:
  build:
    runs-on: ${{ inputs.runner }}
    steps:
      - uses: actions/checkout@v4

      - name: SSH tunnel to registry
        run: |
          mkdir -p ~/.ssh
          echo "${{ secrets.NVOI_SSH_KEY }}" > ~/.ssh/id_rsa
          chmod 600 ~/.ssh/id_rsa
          ssh -N -f -o StrictHostKeyChecking=no \
            -L 5000:${{ inputs.registry }} \
            deploy@${{ inputs.master_ip }} \
            -i ~/.ssh/id_rsa

      - uses: docker/setup-buildx-action@v3
        with:
          driver-opts: network=host

      - uses: actions/github-script@v7
        with:
          script: |
            Object.keys(process.env)
              .filter(k => k.startsWith('ACTIONS_'))
              .forEach(k => utils.exportVariable(k, process.env[k]));

      - name: Build and push
        run: |
          docker buildx build \
            --tag localhost:5000/${{ inputs.name }}:${{ inputs.tag }} \
            --platform ${{ inputs.platform }} \
            --cache-from type=gha \
            --cache-to type=gha,mode=max \
            --output type=image,push=true,registry.insecure=true \
            .
`

// Builder builds images via GitHub Actions and pushes to the cluster registry.
type Builder struct{}

func (b *Builder) Build(ctx context.Context, req provider.BuildRequest) (*provider.BuildResult, error) {
	if req.GitToken == "" {
		return nil, fmt.Errorf("github builder requires git authentication — set GITHUB_TOKEN or --git-token")
	}

	owner, repo, err := parseRepo(req.Source)
	if err != nil {
		return nil, err
	}

	client := newClient(req.GitToken)
	registryAddr := utils.RegistryAddr(req.RegistrySSH.MasterPrivateIP)
	tag := time.Now().UTC().Format("20060102-150405")

	// Runner selection based on platform
	runner := "ubuntu-latest"
	if req.Platform == "linux/arm64" {
		runner = "ubuntu-24.04-arm"
	}

	// 1. Ensure SSH key secret on repo
	fmt.Fprintf(req.Stdout, "  ensuring SSH key secret on %s/%s...\n", owner, repo)
	if err := client.EnsureSecret(ctx, owner, repo, "NVOI_SSH_KEY", string(req.RegistrySSH.PrivKey)); err != nil {
		return nil, fmt.Errorf("set SSH key secret: %w", err)
	}

	// 2. Ensure workflow file in repo
	fmt.Fprintf(req.Stdout, "  ensuring workflow in %s/%s...\n", owner, repo)
	if err := client.EnsureWorkflow(ctx, owner, repo, workflowPath, workflowYAML); err != nil {
		return nil, fmt.Errorf("ensure workflow: %w", err)
	}

	// 3. Trigger workflow
	fmt.Fprintf(req.Stdout, "  triggering build (runner: %s, platform: %s)...\n", runner, req.Platform)
	inputs := map[string]string{
		"name":      req.ServiceName,
		"registry":  registryAddr,
		"master_ip": req.RegistrySSH.MasterIP,
		"platform":  req.Platform,
		"tag":       tag,
		"runner":    runner,
	}
	runID, err := client.TriggerWorkflow(ctx, owner, repo, workflowPath, inputs)
	if err != nil {
		return nil, fmt.Errorf("trigger workflow: %w", err)
	}

	// 4. Wait for completion
	fmt.Fprintf(req.Stdout, "  waiting for GitHub Actions run #%d...\n", runID)
	if err := client.WaitForRun(ctx, owner, repo, runID, req.Stdout); err != nil {
		return nil, err
	}

	imageRef := fmt.Sprintf("%s/%s:%s", registryAddr, req.ServiceName, tag)
	fmt.Fprintf(req.Stdout, "  ✓ pushed %s\n", imageRef)
	return &provider.BuildResult{ImageRef: imageRef}, nil
}

// parseRepo extracts owner/repo from source formats:
// "org/repo", "https://github.com/org/repo.git", "git@github.com:org/repo.git"
func parseRepo(source string) (string, string, error) {
	s := source
	s = strings.TrimPrefix(s, "https://github.com/")
	s = strings.TrimPrefix(s, "http://github.com/")
	s = strings.TrimPrefix(s, "git@github.com:")
	s = strings.TrimSuffix(s, ".git")

	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("cannot parse GitHub repo from source %q — expected org/repo format", source)
	}
	// Strip any path components beyond owner/repo (e.g. org/repo/tree/main)
	repo := parts[1]
	if idx := strings.Index(repo, "/"); idx >= 0 {
		repo = repo[:idx]
	}
	return parts[0], repo, nil
}
