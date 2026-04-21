// Package infra handles SSH connections, server bootstrap, k3s installation, Docker setup, and cloud-init rendering.
package infra

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/getnvoi/nvoi/pkg/utils"
	"gopkg.in/yaml.v3"
)

type cloudConfig struct {
	Hostname    string      `yaml:"hostname,omitempty"`
	Users       []cloudUser `yaml:"users"`
	Packages    []string    `yaml:"packages,omitempty"`
	WriteFiles  []cloudFile `yaml:"write_files,omitempty"`
	RunCmd      []string    `yaml:"runcmd,omitempty"`
	DisableRoot bool        `yaml:"disable_root"`
	SSHPwAuth   bool        `yaml:"ssh_pwauth"`
}

// cloudFile is one entry in cloud-init's write_files block — used by the
// builder cloud-init to drop /etc/docker/daemon.json without the shell-
// escaping gymnastics a runcmd heredoc would need.
type cloudFile struct {
	Path        string `yaml:"path"`
	Content     string `yaml:"content"`
	Permissions string `yaml:"permissions,omitempty"`
	Owner       string `yaml:"owner,omitempty"`
}

type cloudUser struct {
	Name              string   `yaml:"name"`
	Sudo              string   `yaml:"sudo"`
	Shell             string   `yaml:"shell"`
	LockPasswd        bool     `yaml:"lock_passwd"`
	SSHAuthorizedKeys []string `yaml:"ssh_authorized_keys,omitempty"`
}

// SwapSize calculates proportional swap: ~5% of disk, clamped 512MB–2GB.
func SwapSize(diskGB int) int {
	if diskGB <= 0 {
		diskGB = 20
	}
	mb := diskGB * 1024 * 5 / 100
	if mb < 512 {
		mb = 512
	}
	if mb > 2048 {
		mb = 2048
	}
	return mb
}

// EnsureSwap sets up swap via SSH. Reads actual disk size from the server.
// Idempotent — skips if swap is already active. Streams progress to w.
func EnsureSwap(ctx context.Context, ssh utils.SSHClient, w io.Writer) error {
	// Already has swap?
	out, err := ssh.Run(ctx, "swapon --show --noheadings")
	if err == nil && len(strings.TrimSpace(string(out))) > 0 {
		fmt.Fprintln(w, "swap already active")
		return nil
	}

	// Read actual disk size
	out, err = ssh.Run(ctx, "df --output=size / | tail -1")
	if err != nil {
		return nil
	}
	var diskKB int
	fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &diskKB)
	diskGB := diskKB / 1024 / 1024
	swapMB := SwapSize(diskGB)
	fmt.Fprintf(w, "allocating %dMB swap on /swapfile...\n", swapMB)

	steps := []struct {
		label string
		cmd   string
	}{
		{"allocating /swapfile", fmt.Sprintf("fallocate -l %dM /swapfile", swapMB)},
		{"chmod 600 /swapfile", "chmod 600 /swapfile"},
		{"mkswap /swapfile", "mkswap /swapfile"},
		{"swapon /swapfile", "swapon /swapfile"},
		{"registering in /etc/fstab", "bash -c \"grep -q '/swapfile' /etc/fstab || echo '/swapfile none swap sw 0 0' >> /etc/fstab\""},
	}
	for _, s := range steps {
		fmt.Fprintln(w, s.label+"...")
		if err := ssh.RunStream(ctx, "sudo "+s.cmd, w, w); err != nil {
			return fmt.Errorf("swap setup: %s: %w", s.label, err)
		}
	}
	return nil
}

// RenderCloudInit produces cloud-init user-data for server provisioning.
// hostname sets the machine hostname (and therefore the k3s node name).
func RenderCloudInit(sshPublicKey, hostname string) (string, error) {
	user := utils.DefaultUser
	cfg := cloudConfig{
		Hostname: hostname,
		Users: []cloudUser{{
			Name:              user,
			Sudo:              "ALL=(ALL) NOPASSWD:ALL",
			Shell:             "/bin/bash",
			LockPasswd:        true,
			SSHAuthorizedKeys: []string{sshPublicKey},
		}},
		Packages: []string{"git", "curl", "unzip"},
		RunCmd: []string{
			fmt.Sprintf("mkdir -p /home/%s/workspace", user),
			fmt.Sprintf("chown -R %s:%s /home/%s", user, user, user),
		},
		DisableRoot: true,
		SSHPwAuth:   false,
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("cloud-init: %w", err)
	}
	return "#cloud-config\n" + string(data), nil
}

// RenderBuilderCloudInit produces cloud-init user-data for a role: builder
// server. Distinct from RenderCloudInit (which installs k3s) — the builder
// is NOT a k8s node; it exists solely so the SSH BuildProvider
// (pkg/provider/build/ssh) can clone the operator's source on it and run
// `docker buildx build --push`. What it installs:
//
//   - Docker CE + buildx plugin (official apt repo) — the substrate for
//     `docker buildx build --push`, invoked by the SSH BuildProvider over
//     an SSH session.
//   - git — the SSH BuildProvider clones BuildRequest.GitRemote @ GitRef
//     on the builder per-build (shallow, SHA-fetched). Missing git here
//     is the only reason a fresh builder would fail on first dispatch.
//   - xfsprogs — the cache volume is XFS-formatted by pkg/infra/volume.go
//     post-provision (blkid-gated mkfs so we never reformat on re-deploy).
//   - /etc/docker/daemon.json pointing data-root at BuilderCacheMountPath
//     so buildkit layer cache lands on the dedicated cache volume.
//   - Docker auto-start is DISABLED here and enabled post-mount by the
//     provider. If Docker started before the cache volume is mounted, it
//     would populate /var/lib/nvoi/builder-cache on the root disk, then
//     the mount would hide that state (harmless but wasteful). Provider
//     orchestration is: apt install → daemon.json → MountVolume →
//     systemctl enable --now docker.
//
// What's NOT installed: nvoi itself. Unlike an earlier design iteration
// that dispatched `nvoi deploy --local` to the builder (requiring the
// binary on PATH), the PR-B SSH BuildProvider runs docker directly over
// SSH. The builder is a generic docker+git host, not an nvoi runtime.
//
// No k3s — builder never joins the cluster. No worker firewall attach —
// builder firewall is its own (SSH-from-anywhere + egress).
func RenderBuilderCloudInit(sshPublicKey, hostname string) (string, error) {
	user := utils.DefaultUser
	// daemon.json lives on the root disk before the cache volume mounts.
	// cloud-init's write_files block drops the file atomically before runcmd
	// starts — no shell-escaping gymnastics (which fight both Go's %q and
	// YAML quote escaping).
	daemonJSON := fmt.Sprintf("{\"data-root\": %q}\n", utils.BuilderCacheMountPath)
	cfg := cloudConfig{
		Hostname: hostname,
		Users: []cloudUser{{
			Name:              user,
			Sudo:              "ALL=(ALL) NOPASSWD:ALL",
			Shell:             "/bin/bash",
			LockPasswd:        true,
			SSHAuthorizedKeys: []string{sshPublicKey},
		}},
		// git: the SSH BuildProvider clones BuildRequest.GitRemote on the
		// builder per-build. Missing git here = `command not found` on
		// every dispatch.
		Packages: []string{"ca-certificates", "curl", "git", "gnupg", "xfsprogs"},
		WriteFiles: []cloudFile{{
			Path:        "/etc/docker/daemon.json",
			Content:     daemonJSON,
			Permissions: "0644",
			Owner:       "root:root",
		}},
		RunCmd: []string{
			fmt.Sprintf("mkdir -p /home/%s/workspace", user),
			fmt.Sprintf("chown -R %s:%s /home/%s", user, user, user),
			// Install Docker CE from the official apt repo. The `. /etc/os-release` is
			// evaluated by the shell at runtime so the same cloud-init works on any
			// Ubuntu codename (noble, jammy, ...) — nvoi pins DefaultImage but the
			// exact codename is a cloud provider detail we don't need to track here.
			"install -m 0755 -d /etc/apt/keyrings",
			"curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc",
			"chmod a+r /etc/apt/keyrings/docker.asc",
			`sh -c 'echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo $VERSION_CODENAME) stable" > /etc/apt/sources.list.d/docker.list'`,
			"apt-get update",
			"DEBIAN_FRONTEND=noninteractive apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin",
			// Stop Docker (apt auto-started it) and disable auto-start. Provider's
			// post-mount step re-enables it after the cache volume is mounted at
			// BuilderCacheMountPath — the data-root Docker will use going forward.
			"systemctl stop docker.service docker.socket",
			"systemctl disable docker.service docker.socket",
			fmt.Sprintf("mkdir -p %s", utils.BuilderCacheMountPath),
			// Add operator user to docker group so the SSH BuildProvider can
			// run `docker buildx` without sudo over the dispatch SSH session.
			fmt.Sprintf("usermod -aG docker %s", user),
		},
		DisableRoot: true,
		SSHPwAuth:   false,
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("builder cloud-init: %w", err)
	}
	return "#cloud-config\n" + string(data), nil
}
