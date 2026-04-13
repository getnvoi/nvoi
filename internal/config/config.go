// Package config holds shared types used by reconcile, packages, and the CLI.
// No logic — just data structures.
package config

import (
	"fmt"
	"sort"

	app "github.com/getnvoi/nvoi/pkg/core"
	"gopkg.in/yaml.v3"
)

// DeployContext holds everything needed to execute against a cluster.
type DeployContext struct {
	Cluster     app.Cluster
	DNS         app.ProviderRef
	Storage     app.ProviderRef
	Builder     string
	BuildCreds  map[string]string
	GitUsername string
	GitToken    string
}

// LiveState represents what's currently deployed.
type LiveState struct {
	Servers    []string
	ServerDisk map[string]int // server short name → root disk GB (from provider)
	Services   []string
	Crons      []string
	Volumes    []string
	Storage    []string
	Secrets    []string
	Domains    map[string][]string
}

type AppConfig struct {
	App       string                 `yaml:"app"`
	Env       string                 `yaml:"env"`
	Providers ProvidersDef           `yaml:"providers"`
	Servers   map[string]ServerDef   `yaml:"servers"`
	Firewall  []string               `yaml:"-"`
	Volumes   map[string]VolumeDef   `yaml:"volumes,omitempty"`
	Database  map[string]DatabaseDef `yaml:"database,omitempty"`
	Build     map[string]string      `yaml:"build,omitempty"`
	Secrets   []string               `yaml:"secrets,omitempty"`
	Storage   map[string]StorageDef  `yaml:"storage,omitempty"`
	Services  map[string]ServiceDef  `yaml:"services"`
	Crons     map[string]CronDef     `yaml:"crons,omitempty"`
	Domains   map[string][]string    `yaml:"domains,omitempty"`
	ACMEEmail string                 `yaml:"acme_email,omitempty"`
}

// DatabaseNames returns the names of all configured databases.
func (c *AppConfig) DatabaseNames() []string {
	if c == nil {
		return nil
	}
	names := make([]string, 0, len(c.Database))
	for n := range c.Database {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

type DatabaseDef struct {
	Image  string    `yaml:"image"`
	Volume string    `yaml:"volume"`
	Backup BackupDef `yaml:"backup,omitempty"`
}

type BackupDef struct {
	Schedule string `yaml:"schedule,omitempty"` // default: "0 */6 * * *"
	Retain   int    `yaml:"retain,omitempty"`   // default: 7
}

type ProvidersDef struct {
	Compute string `yaml:"compute"`
	DNS     string `yaml:"dns,omitempty"`
	Storage string `yaml:"storage,omitempty"`
	Build   string `yaml:"build,omitempty"`
}

type ServerDef struct {
	Type   string `yaml:"type"`
	Region string `yaml:"region"`
	Role   string `yaml:"role"`
	Disk   int    `yaml:"disk,omitempty"` // root disk size in GB (0 = provider default)
}

type VolumeDef struct {
	Size   int    `yaml:"size"`
	Server string `yaml:"server"`
}

type StorageDef struct {
	CORS       bool   `yaml:"cors,omitempty"`
	ExpireDays int    `yaml:"expire_days,omitempty"`
	Bucket     string `yaml:"bucket,omitempty"`
}

type ServiceDef struct {
	Image    string   `yaml:"image,omitempty"`
	Build    string   `yaml:"build,omitempty"`
	Port     int      `yaml:"port,omitempty"`
	Replicas int      `yaml:"replicas,omitempty"`
	Command  string   `yaml:"command,omitempty"`
	Health   string   `yaml:"health,omitempty"`
	Server   string   `yaml:"server,omitempty"`
	Servers  []string `yaml:"servers,omitempty"`
	Env      []string `yaml:"env,omitempty"`
	Secrets  []string `yaml:"secrets,omitempty"`
	Storage  []string `yaml:"storage,omitempty"`
	Volumes  []string `yaml:"volumes,omitempty"`
}

type CronDef struct {
	Image    string   `yaml:"image,omitempty"`
	Build    string   `yaml:"build,omitempty"`
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
