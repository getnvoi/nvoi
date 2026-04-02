package local

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/infra"
	"github.com/getnvoi/nvoi/pkg/provider"
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

	// Fix file permissions before build — Docker COPY preserves filesystem permissions.
	// Files with 600 become unreadable by the container's non-root user.
	fmt.Fprintf(req.Stdout, "  fixing file permissions...\n")
	if err := fixPermissions(req.Source); err != nil {
		return nil, fmt.Errorf("fix permissions: %w", err)
	}

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

// fixPermissions ensures all files are 644 and all directories are 755
// in the source tree. Skips .git and respects .dockerignore implicitly
// (Docker handles that). Executable files in bin/ are set to 755.
func fixPermissions(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip .git directory
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		if d.IsDir() {
			if info.Mode().Perm() != 0755 {
				return os.Chmod(path, 0755)
			}
			return nil
		}

		// bin/* files → 755, everything else → 644
		rel, _ := filepath.Rel(root, path)
		if strings.HasPrefix(rel, "bin/") || strings.HasPrefix(rel, "bin"+string(filepath.Separator)) {
			if info.Mode().Perm() != 0755 {
				return os.Chmod(path, 0755)
			}
		} else {
			if info.Mode().Perm() != 0644 {
				return os.Chmod(path, 0644)
			}
		}
		return nil
	})
}
