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
