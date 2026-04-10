// Package infra handles SSH connections, server bootstrap, k3s installation, Docker setup, and cloud-init rendering.
package infra

import (
	"context"
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/pkg/utils"
	"gopkg.in/yaml.v3"
)

type cloudConfig struct {
	Hostname    string      `yaml:"hostname,omitempty"`
	Users       []cloudUser `yaml:"users"`
	Packages    []string    `yaml:"packages,omitempty"`
	RunCmd      []string    `yaml:"runcmd,omitempty"`
	DisableRoot bool        `yaml:"disable_root"`
	SSHPwAuth   bool        `yaml:"ssh_pwauth"`
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
// Idempotent — skips if swap is already active.
func EnsureSwap(ctx context.Context, ssh utils.SSHClient) error {
	// Already has swap?
	out, err := ssh.Run(ctx, "swapon --show --noheadings")
	if err == nil && len(strings.TrimSpace(string(out))) > 0 {
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

	cmds := []string{
		fmt.Sprintf("fallocate -l %dM /swapfile", swapMB),
		"chmod 600 /swapfile",
		"mkswap /swapfile",
		"swapon /swapfile",
		"grep -q '/swapfile' /etc/fstab || echo '/swapfile none swap sw 0 0' >> /etc/fstab",
	}
	for _, cmd := range cmds {
		if _, err := ssh.Run(ctx, "sudo "+cmd); err != nil {
			return fmt.Errorf("swap setup: %s: %w", cmd, err)
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
