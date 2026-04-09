package reconcile

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

type AppConfig struct {
	App       string                `yaml:"app"`
	Env       string                `yaml:"env"`
	Providers ProvidersDef          `yaml:"providers"`
	Servers   map[string]ServerDef  `yaml:"servers"`
	Firewall  []string              `yaml:"-"`
	Volumes   map[string]VolumeDef  `yaml:"volumes,omitempty"`
	Build     map[string]string     `yaml:"build,omitempty"`
	Secrets   []string              `yaml:"secrets,omitempty"`
	Storage   map[string]StorageDef `yaml:"storage,omitempty"`
	Services  map[string]ServiceDef `yaml:"services"`
	Crons     map[string]CronDef    `yaml:"crons,omitempty"`
	Domains   map[string][]string   `yaml:"domains,omitempty"`
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
	Env      []string `yaml:"env,omitempty"`
	Secrets  []string `yaml:"secrets,omitempty"`
	Storage  []string `yaml:"storage,omitempty"`
	Volumes  []string `yaml:"volumes,omitempty"`
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
