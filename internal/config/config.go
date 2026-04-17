// Package config holds shared types used by reconcile and the CLI.
// No logic — just data structures.
package config

import (
	"fmt"
	"strings"

	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
	"gopkg.in/yaml.v3"
)

// DeployContext holds everything needed to execute against a cluster.
type DeployContext struct {
	Cluster app.Cluster
	DNS     app.ProviderRef
	Storage app.ProviderRef
	Creds   provider.CredentialSource // single source for all credential resolution at runtime
}

// LiveState represents what's currently deployed.
type LiveState struct {
	Servers    []string
	ServerDisk map[string]int // server short name → root disk GB (from provider)
	Firewalls  []string
	Services   []string
	Crons      []string
	Volumes    []string
	Storage    []string
	Domains    map[string][]string
}

type AppConfig struct {
	App       string                `yaml:"app"`
	Env       string                `yaml:"env"`
	Providers ProvidersDef          `yaml:"providers"`
	Servers   map[string]ServerDef  `yaml:"servers"`
	Firewall  []string              `yaml:"-"`
	Volumes   map[string]VolumeDef  `yaml:"volumes,omitempty"`
	Secrets   []string              `yaml:"secrets,omitempty"`
	Storage   map[string]StorageDef `yaml:"storage,omitempty"`
	Services  map[string]ServiceDef `yaml:"services"`
	Crons     map[string]CronDef    `yaml:"crons,omitempty"`
	Domains   map[string][]string   `yaml:"domains,omitempty"`
	ACMEEmail string                `yaml:"acme_email,omitempty"`

	// Resolved firewall names — populated by Resolve()
	MasterFirewall string `yaml:"-"`
	WorkerFirewall string `yaml:"-"`
}

// StorageNames returns all user-declared storage bucket names.
func (c *AppConfig) StorageNames() []string {
	names := make([]string, 0, len(c.Storage))
	for name := range c.Storage {
		names = append(names, name)
	}
	return names
}

// ServiceSecrets returns a map of service/cron name → secret key names.
// This is the source of truth for "which secrets are on which workload."
func (c *AppConfig) ServiceSecrets() map[string][]string {
	m := make(map[string][]string)
	extract := func(refs []string) []string {
		keys := make([]string, len(refs))
		for i, ref := range refs {
			if k, _, ok := strings.Cut(ref, "="); ok {
				keys[i] = k
			} else {
				keys[i] = ref
			}
		}
		return keys
	}
	for name, svc := range c.Services {
		if len(svc.Secrets) > 0 {
			m[name] = extract(svc.Secrets)
		}
	}
	for name, cron := range c.Crons {
		if len(cron.Secrets) > 0 {
			m[name] = extract(cron.Secrets)
		}
	}
	return m
}

// Resolve populates all computed fields on VolumeDef and firewall names.
// Called once after ValidateConfig. Internal code trusts resolved values.
// Derivation functions live in pkg/utils/naming.go — single source of truth.
func (c *AppConfig) Resolve() error {
	names, err := utils.NewNames(c.App, c.Env)
	if err != nil {
		return err
	}

	// Volume mount paths — one derivation, stored in config.
	for volName, vol := range c.Volumes {
		vol.MountPath = names.VolumeMountPath(volName)
		c.Volumes[volName] = vol
	}

	// Firewall names — per-role.
	c.MasterFirewall = names.MasterFirewall()
	c.WorkerFirewall = names.WorkerFirewall()

	return nil
}

type ProvidersDef struct {
	Compute string `yaml:"compute"`
	DNS     string `yaml:"dns,omitempty"`
	Storage string `yaml:"storage,omitempty"`
}

type ServerDef struct {
	Type   string `yaml:"type"`
	Region string `yaml:"region"`
	Role   string `yaml:"role"`
	Disk   int    `yaml:"disk,omitempty"` // root disk size in GB (0 = provider default)
}

type VolumeDef struct {
	Size      int    `yaml:"size"`
	Server    string `yaml:"server"`
	MountPath string `yaml:"-"` // resolved: /mnt/data/{base}-{name}
}

type StorageDef struct {
	CORS       bool   `yaml:"cors,omitempty"`
	ExpireDays int    `yaml:"expire_days,omitempty"`
	Bucket     string `yaml:"bucket,omitempty"`
}

type ServiceDef struct {
	Image     string   `yaml:"image,omitempty"`
	Port      int      `yaml:"port,omitempty"`
	Replicas  int      `yaml:"replicas,omitempty"`
	Command   string   `yaml:"command,omitempty"`
	Health    string   `yaml:"health,omitempty"`
	Server    string   `yaml:"server,omitempty"`
	Servers   []string `yaml:"servers,omitempty"`
	Env       []string `yaml:"env,omitempty"`
	Secrets   []string `yaml:"secrets,omitempty"`
	Storage   []string `yaml:"storage,omitempty"`
	Volumes   []string `yaml:"volumes,omitempty"`
	DependsOn []string `yaml:"depends_on,omitempty"` // other services that must be Ready before this one is applied
}

type CronDef struct {
	Image    string   `yaml:"image,omitempty"`
	Schedule string   `yaml:"schedule"`
	Command  string   `yaml:"command,omitempty"`
	Server   string   `yaml:"server,omitempty"`
	Servers  []string `yaml:"servers,omitempty"`
	Env      []string `yaml:"env,omitempty"`
	Secrets  []string `yaml:"secrets,omitempty"`
	Storage  []string `yaml:"storage,omitempty"`
	Volumes  []string `yaml:"volumes,omitempty"`
}

// ResolvedServers returns the effective server list for a service.
func (s ServiceDef) ResolvedServers() []string {
	if len(s.Servers) > 0 {
		return s.Servers
	}
	if s.Server != "" {
		return []string{s.Server}
	}
	return nil
}

// ResolvedServers returns the effective server list for a cron.
func (c CronDef) ResolvedServers() []string {
	if len(c.Servers) > 0 {
		return c.Servers
	}
	if c.Server != "" {
		return []string{c.Server}
	}
	return nil
}

// NamedServer pairs a server definition with its map key name.
type NamedServer struct {
	Name string
	ServerDef
}

func (c *AppConfig) UnmarshalYAML(value *yaml.Node) error {
	type plain AppConfig
	if err := value.Decode((*plain)(c)); err != nil {
		return err
	}
	if value.Kind == yaml.MappingNode {
		for i := 0; i < len(value.Content)-1; i += 2 {
			if value.Content[i].Value == "firewall" {
				fw := value.Content[i+1]
				switch fw.Kind {
				case yaml.ScalarNode:
					c.Firewall = []string{fw.Value}
				case yaml.SequenceNode:
					var list []string
					if err := fw.Decode(&list); err != nil {
						return fmt.Errorf("firewall: %w", err)
					}
					c.Firewall = list
				}
			}
		}
	}
	return nil
}

func ParseAppConfig(data []byte) (*AppConfig, error) {
	var cfg AppConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func MarshalAppConfig(cfg *AppConfig) ([]byte, error) {
	return yaml.Marshal(cfg)
}
