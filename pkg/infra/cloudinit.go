// Package infra handles SSH connections, server bootstrap, k3s installation, Docker setup, and cloud-init rendering.
package infra

import (
	"fmt"

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
func SwapSize(rootVolumeSizeGB int) int {
	if rootVolumeSizeGB <= 0 {
		rootVolumeSizeGB = 20
	}
	mb := rootVolumeSizeGB * 1024 * 5 / 100 // 5% of disk in MB
	if mb < 512 {
		mb = 512
	}
	if mb > 2048 {
		mb = 2048
	}
	return mb
}

// RenderCloudInit produces cloud-init user-data for server provisioning.
// hostname sets the machine hostname (and therefore the k3s node name).
// rootVolumeSizeGB is used to calculate proportional swap (0 = default 20GB).
func RenderCloudInit(sshPublicKey, hostname string, rootVolumeSizeGB int) (string, error) {
	user := utils.DefaultUser
	swapMB := SwapSize(rootVolumeSizeGB)
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
			fmt.Sprintf("fallocate -l %dM /swapfile", swapMB),
			"chmod 600 /swapfile",
			"mkswap /swapfile",
			"swapon /swapfile",
			"echo '/swapfile none swap sw 0 0' >> /etc/fstab",
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
