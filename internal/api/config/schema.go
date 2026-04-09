// Package config defines the public config schema for nvoi repos.
//
// A config is a YAML document that declaratively describes everything needed
// to deploy an application: servers, volumes, builds, storage, services, and
// domains. The orchestrator reads a config and executes the corresponding
// pkg/core/ functions in order — same sequence as bin/deploy-* scripts.
package config

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

// FirewallConfig supports presets and/or explicit port rules.
//
//	firewall: default                    # string preset
//	firewall: cloudflare                 # string preset
//	firewall:                            # preset + overrides
//	  preset: cloudflare
//	  443: [0.0.0.0/0]
//	firewall:                            # explicit only
//	  80: [0.0.0.0/0]
//	  443: [0.0.0.0/0]
type FirewallConfig struct {
	Preset string              `json:"preset,omitempty" yaml:"preset,omitempty"`
	Rules  map[string][]string `json:"rules,omitempty" yaml:"rules,omitempty"` // port → CIDRs
}

func (f *FirewallConfig) UnmarshalYAML(value *yaml.Node) error {
	// String → preset only
	if value.Kind == yaml.ScalarNode {
		f.Preset = value.Value
		return nil
	}
	// Mapping: check for "preset" key, everything else is port rules
	if value.Kind == yaml.MappingNode {
		f.Rules = map[string][]string{}
		for i := 0; i < len(value.Content)-1; i += 2 {
			key := value.Content[i].Value
			val := value.Content[i+1]
			if key == "preset" {
				f.Preset = val.Value
			} else {
				var cidrs []string
				if err := val.Decode(&cidrs); err != nil {
					return err
				}
				f.Rules[key] = cidrs
			}
		}
		if len(f.Rules) == 0 {
			f.Rules = nil
		}
		return nil
	}
	return fmt.Errorf("firewall must be a string preset or mapping")
}

func (f FirewallConfig) MarshalJSON() ([]byte, error) {
	if f.Preset != "" && len(f.Rules) == 0 {
		return json.Marshal(f.Preset)
	}
	m := make(map[string]any, len(f.Rules)+1)
	if f.Preset != "" {
		m["preset"] = f.Preset
	}
	for port, cidrs := range f.Rules {
		m[port] = cidrs
	}
	return json.Marshal(m)
}

func (f *FirewallConfig) UnmarshalJSON(data []byte) error {
	// Try string first
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		f.Preset = s
		return nil
	}
	// Try map with optional "preset" key
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	f.Rules = map[string][]string{}
	for key, val := range raw {
		if key == "preset" {
			if err := json.Unmarshal(val, &f.Preset); err != nil {
				return fmt.Errorf("firewall preset must be a string: %w", err)
			}
		} else {
			var cidrs []string
			if err := json.Unmarshal(val, &cidrs); err != nil {
				return err
			}
			f.Rules[key] = cidrs
		}
	}
	if len(f.Rules) == 0 {
		f.Rules = nil
	}
	return nil
}

// DomainConfig supports structured domains config.
//
//	domains:
//	  web:
//	    domains: [example.com, www.example.com]
type DomainConfig struct {
	Domains []string `json:"domains" yaml:"domains"`
}

// IngressConfig controls TLS and edge proxy behavior.
//
//	ingress:
//	  cloudflare-managed: true
//
//	ingress:
//	  cert: TLS_CERT_PEM
//	  key: TLS_KEY_PEM
//
// Omit ingress entirely for ACME (Caddy auto-certs).
type IngressConfig struct {
	CloudflareManaged bool   `json:"cloudflare-managed,omitempty" yaml:"cloudflare-managed,omitempty"`
	Cert              string `json:"cert,omitempty" yaml:"cert,omitempty"` // env key containing PEM
	Key               string `json:"key,omitempty" yaml:"key,omitempty"`   // env key containing PEM
}

// Workload holds the fields shared by Service and Cron.
// Both run containers with the same image/command/env/secret/storage/volume model.
type Workload struct {
	Image   string   `json:"image,omitempty" yaml:"image,omitempty"`
	Command string   `json:"command,omitempty" yaml:"command,omitempty"`
	Server  string   `json:"server,omitempty" yaml:"server,omitempty"`
	Env     []string `json:"env,omitempty" yaml:"env,omitempty"`
	Secrets []string `json:"secrets,omitempty" yaml:"secrets,omitempty"`
	Storage []string `json:"storage,omitempty" yaml:"storage,omitempty"`
	Volumes []string `json:"volumes,omitempty" yaml:"volumes,omitempty"`
}

// Cron is a scheduled workload (CronJob).
type Cron struct {
	Workload `yaml:",inline"`
	Schedule string `json:"schedule" yaml:"schedule"`
}

// Config is the public schema — what users write.
type Config struct {
	Servers  map[string]Server  `json:"servers" yaml:"servers"`
	Firewall *FirewallConfig    `json:"firewall,omitempty" yaml:"firewall,omitempty"`
	Volumes  map[string]Volume  `json:"volumes,omitempty" yaml:"volumes,omitempty"`
	Build    map[string]Build   `json:"build,omitempty" yaml:"build,omitempty"`
	Storage  map[string]Storage `json:"storage,omitempty" yaml:"storage,omitempty"`
	Services map[string]Service `json:"services" yaml:"services"`
	Crons    map[string]Cron    `json:"crons,omitempty" yaml:"crons,omitempty"`
	Domains  map[string]Domains `json:"domains,omitempty" yaml:"domains,omitempty"`
	Ingress  *IngressConfig     `json:"ingress,omitempty" yaml:"ingress,omitempty"`
}

type Server struct {
	Type   string `json:"type" yaml:"type"`
	Region string `json:"region" yaml:"region"`
	Role   string `json:"role" yaml:"role"` // "master" or "worker" — required
}

type Volume struct {
	Size   int    `json:"size" yaml:"size"`     // GB
	Server string `json:"server" yaml:"server"` // must reference a defined server
}

type Build struct {
	Source string `json:"source" yaml:"source"` // git repo (org/repo) or local path
}

type Storage struct {
	CORS       bool   `json:"cors,omitempty" yaml:"cors,omitempty"`
	ExpireDays int    `json:"expire_days,omitempty" yaml:"expire_days,omitempty"`
	Bucket     string `json:"bucket,omitempty" yaml:"bucket,omitempty"` // override auto-generated name
}

type Service struct {
	Workload      `yaml:",inline"`
	Managed       string   `json:"managed,omitempty" yaml:"managed,omitempty"`               // managed service kind (postgres, redis, meilisearch)
	Port          int      `json:"port,omitempty" yaml:"port,omitempty"`                     // exposed port
	Replicas      int      `json:"replicas,omitempty" yaml:"replicas,omitempty"`             // default 1
	Health        string   `json:"health,omitempty" yaml:"health,omitempty"`                 // readiness probe path
	Uses          []string `json:"uses,omitempty" yaml:"uses,omitempty"`                     // managed service refs → credentials injected as secrets
	VolumeSize    int      `json:"volume_size,omitempty" yaml:"volume_size,omitempty"`       // data volume size in GB (managed services)
	BackupStorage string   `json:"backup_storage,omitempty" yaml:"backup_storage,omitempty"` // pre-existing storage name for backups
	BackupCron    string   `json:"backup_cron,omitempty" yaml:"backup_cron,omitempty"`       // backup schedule
}

// Domains supports a single string, a list of strings, or a structured form with domains.
//
//	domains:
//	  web: example.com                   # simple string
//	  api: [a.com, b.com]               # list
//	  admin:                             # structured
//	    domains: [admin.example.com]
type Domains []string

func (d *Domains) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		*d = Domains{value.Value}
		return nil
	}
	if value.Kind == yaml.SequenceNode {
		var list []string
		if err := value.Decode(&list); err != nil {
			return err
		}
		*d = list
		return nil
	}
	// Mapping — structured form.
	if value.Kind == yaml.MappingNode {
		for i := 0; i < len(value.Content)-1; i += 2 {
			if key := value.Content[i].Value; key != "domains" {
				return fmt.Errorf("domains mapping only supports the \"domains\" key")
			}
		}
		var dc DomainConfig
		if err := value.Decode(&dc); err != nil {
			return err
		}
		*d = Domains(dc.Domains)
		return nil
	}
	return fmt.Errorf("domains entry must be a string, list, or mapping with a domains key")
}

func (d *Domains) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*d = Domains{s}
		return nil
	}
	var list []string
	if err := json.Unmarshal(data, &list); err == nil {
		*d = list
		return nil
	}
	var dc DomainConfig
	if err := json.Unmarshal(data, &dc); err == nil {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err == nil {
			for key := range raw {
				if key != "domains" {
					return fmt.Errorf("domains object only supports the \"domains\" key")
				}
			}
		}
		*d = Domains(dc.Domains)
		return nil
	}
	return fmt.Errorf("domains entry must be a string, list, or object with a domains key")
}

// Parse parses a YAML config.
func Parse(data []byte) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// ParseJSON parses a JSON config.
func ParseJSON(data []byte) (*Config, error) {
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// ParseEnv parses a .env file into a key-value map.
// Delegates to godotenv — handles quoting, escaping, multiline, comments.
func ParseEnv(data string) (map[string]string, error) {
	if data == "" {
		return map[string]string{}, nil
	}
	return godotenv.Parse(strings.NewReader(data))
}
