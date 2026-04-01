package local

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/getnvoi/nvoi/internal/core"
	"github.com/getnvoi/nvoi/internal/infra"
	"github.com/getnvoi/nvoi/internal/provider"
)

// Builder builds images locally with docker buildx and pushes through an SSH tunnel.
type Builder struct{}

func (b *Builder) Build(ctx context.Context, req provider.BuildRequest) (*provider.BuildResult, error) {
	// SSH tunnel to registry
	ssh, err := infra.ConnectSSH(ctx, req.RegistrySSH.MasterIP+":22", core.DefaultUser, req.RegistrySSH.PrivKey)
	if err != nil {
		return nil, fmt.Errorf("ssh for registry tunnel: %w", err)
	}
	defer ssh.Close()

	registryAddr := core.RegistryAddr(req.RegistrySSH.MasterPrivateIP)
	tunnelAddr, cleanup, err := infra.LocalForward(ssh, registryAddr)
	if err != nil {
		return nil, fmt.Errorf("ssh tunnel to registry: %w", err)
	}
	defer cleanup()

	// Auto-detect platform from master if not set
	platform := req.Platform
	if platform == "" {
		out, err := ssh.Run(ctx, "uname -m")
		if err == nil {
			arch := strings.TrimSpace(string(out))
			if arch == "aarch64" || arch == "arm64" {
				platform = "linux/arm64"
			} else {
				platform = "linux/amd64"
			}
		} else {
			platform = "linux/amd64"
		}
	}

	tag := time.Now().UTC().Format("20060102-150405")
	tunnelRef := fmt.Sprintf("%s/%s:%s", tunnelAddr, req.ServiceName, tag)
	registryRef := fmt.Sprintf("%s/%s:%s", registryAddr, req.ServiceName, tag)

	fmt.Fprintf(req.Stdout, "  building %s (platform: %s)...\n", req.ServiceName, platform)

	cmd := exec.CommandContext(ctx, "docker", "buildx", "build",
		"--tag", tunnelRef,
		"--platform", platform,
		"--output", "type=image,push=true,registry.insecure=true",
		req.Source,
	)
	cmd.Stdout = req.Stdout
	cmd.Stderr = req.Stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("docker build: %w", err)
	}

	fmt.Fprintf(req.Stdout, "  ✓ pushed %s\n", registryRef)
	return &provider.BuildResult{ImageRef: registryRef}, nil
}
