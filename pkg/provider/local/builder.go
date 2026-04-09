// Package local implements the build provider using local Docker buildx.
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

	"github.com/getnvoi/nvoi/pkg/infra"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// Builder builds images locally with docker buildx and pushes through an SSH tunnel.
type Builder struct{}

func (b *Builder) Build(ctx context.Context, req provider.BuildRequest) (*provider.BuildResult, error) {
	// SSH tunnel to registry
	ssh, err := infra.ConnectSSH(ctx, req.RegistrySSH.MasterIP+":22", utils.DefaultUser, req.RegistrySSH.PrivKey)
	if err != nil {
		return nil, fmt.Errorf("ssh for registry tunnel: %w", err)
	}
	defer ssh.Close()

	registryAddr := utils.RegistryAddr(req.RegistrySSH.MasterPrivateIP)
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

	// Resolve Dockerfile and build context
	dockerfile, buildContext, err := resolveDockerfile(req.Source, req.Dockerfile)
	if err != nil {
		return nil, err
	}

	// Fix file permissions before build — Docker COPY preserves filesystem permissions.
	// Files with 600 become unreadable by the container's non-root user.
	fmt.Fprintf(req.Stdout, "  fixing file permissions...\n")
	if err := fixPermissions(buildContext); err != nil {
		return nil, fmt.Errorf("fix permissions: %w", err)
	}

	fmt.Fprintf(req.Stdout, "  building %s (platform: %s)...\n", req.ServiceName, platform)

	buildArgs := []string{"buildx", "build",
		"--tag", tunnelRef,
		"--platform", platform,
		"--output", "type=image,push=true,registry.insecure=true",
		"-f", dockerfile,
		buildContext,
	}
	cmd := exec.CommandContext(ctx, "docker", buildArgs...)
	cmd.Stdout = req.Stdout
	cmd.Stderr = req.Stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("docker build: %w", err)
	}

	fmt.Fprintf(req.Stdout, "  ✓ pushed %s\n", registryRef)
	return &provider.BuildResult{ImageRef: registryRef}, nil
}

// resolveDockerfile finds the Dockerfile and build context.
//
// Dockerfile resolution:
//   - --dockerfile-path set: try {source}/{path}, then {path} as-is. Error if neither exists.
//   - --dockerfile-path not set: {source}/Dockerfile. Error if not found.
//
// Build context: walk up from source to find .git or go.mod (project root).
// If source is already the project root, context = source.
func resolveDockerfile(source, dockerfilePath string) (dockerfile, context string, err error) {
	absSource, err := filepath.Abs(source)
	if err != nil {
		return "", "", fmt.Errorf("resolve source path: %w", err)
	}

	if dockerfilePath != "" {
		// Try relative to source first
		rel := filepath.Join(absSource, dockerfilePath)
		if _, err := os.Stat(rel); err == nil {
			dockerfile = rel
		} else {
			// Try as-is (absolute or relative to cwd)
			abs, _ := filepath.Abs(dockerfilePath)
			if _, err := os.Stat(abs); err == nil {
				dockerfile = abs
			} else {
				return "", "", fmt.Errorf("dockerfile not found: tried %s and %s", rel, abs)
			}
		}
	} else {
		// Default: {source}/Dockerfile
		dockerfile = filepath.Join(absSource, "Dockerfile")
		if _, err := os.Stat(dockerfile); err != nil {
			return "", "", fmt.Errorf("no Dockerfile at %s — use --dockerfile-path to specify", dockerfile)
		}
	}

	context = findProjectRoot(absSource)
	return dockerfile, context, nil
}

// findProjectRoot walks up from dir looking for .git or go.mod.
// Returns dir itself if nothing found (source IS the root).
func findProjectRoot(dir string) string {
	current := dir
	for {
		for _, marker := range []string{".git", "go.mod"} {
			if _, err := os.Stat(filepath.Join(current, marker)); err == nil {
				return current
			}
		}
		parent := filepath.Dir(current)
		if parent == current {
			// Hit filesystem root — give up, use source as context
			return dir
		}
		current = parent
	}
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
