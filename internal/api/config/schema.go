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

// DomainConfig supports simple domains or domains with proxy config.
//
//	domains:
//	  web: example.com                   # simple
//	  web: [example.com, www.example.com] # list
//	  web:                               # with proxy
//	    domains: [example.com]
//	    proxy: true
type DomainConfig struct {
	Domains []string `json:"domains" yaml:"domains"`
	Proxy   bool     `json:"proxy,omitempty" yaml:"proxy,omitempty"`
}

// Config is the public schema — what users write.
type Config struct {
	Servers  map[string]Server  `json:"servers" yaml:"servers"`
	Firewall *FirewallConfig    `json:"firewall,omitempty" yaml:"firewall,omitempty"`
	Volumes  map[string]Volume  `json:"volumes,omitempty" yaml:"volumes,omitempty"`
	Build    map[string]Build   `json:"build,omitempty" yaml:"build,omitempty"`
	Storage  map[string]Storage `json:"storage,omitempty" yaml:"storage,omitempty"`
	Services map[string]Service `json:"services" yaml:"services"`
	Domains  map[string]Domains `json:"domains,omitempty" yaml:"domains,omitempty"`
	// DomainProxy lists service names whose domains should be proxied through Cloudflare.
	// Cloudflare-only. Set via structured domain config:
	//   domains:
	//     web:
	//       domains: [example.com]
	//       proxy: true
	// Populated by Parse from DomainConfig entries that have proxy: true.
	DomainProxy map[string]bool `json:"-" yaml:"-"` // internal, not serialized directly
}

type Server struct {
	Type   string `json:"type" yaml:"type"`
	Region string `json:"region" yaml:"region"`
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
	Image    string   `json:"image,omitempty" yaml:"image,omitempty"`       // pre-built image (e.g. postgres:17)
	Build    string   `json:"build,omitempty" yaml:"build,omitempty"`       // references a build target
	Managed  string   `json:"managed,omitempty" yaml:"managed,omitempty"`   // managed service kind (postgres, redis, meilisearch)
	Port     int      `json:"port,omitempty" yaml:"port,omitempty"`         // exposed port
	Replicas int      `json:"replicas,omitempty" yaml:"replicas,omitempty"` // default 1
	Command  string   `json:"command,omitempty" yaml:"command,omitempty"`   // override entrypoint
	Health   string   `json:"health,omitempty" yaml:"health,omitempty"`     // readiness probe path
	Server   string   `json:"server,omitempty" yaml:"server,omitempty"`     // pin to server via node selector
	Volumes  []string `json:"volumes,omitempty" yaml:"volumes,omitempty"`   // name:/path
	Env      []string `json:"env,omitempty" yaml:"env,omitempty"`           // KEY=VALUE or KEY (resolved from .env)
	Secrets  []string `json:"secrets,omitempty" yaml:"secrets,omitempty"`   // k8s secret key refs (resolved from .env)
	Storage  []string `json:"storage,omitempty" yaml:"storage,omitempty"`   // storage name refs → STORAGE_{NAME}_*
	Uses     []string `json:"uses,omitempty" yaml:"uses,omitempty"`         // managed service refs → credentials injected as secrets
}

// Domains supports a single string, a list of strings, or a structured form with proxy.
//
//	domains:
//	  web: example.com                   # simple string
//	  api: [a.com, b.com]               # list
//	  admin:                             # structured (Cloudflare proxy)
//	    domains: [admin.example.com]
//	    proxy: true
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
	// Mapping — structured form. Domains extracted here, proxy handled by Parse.
	if value.Kind == yaml.MappingNode {
		var dc DomainConfig
		if err := value.Decode(&dc); err != nil {
			return err
		}
		*d = Domains(dc.Domains)
		return nil
	}
	return fmt.Errorf("domains entry must be a string, list, or mapping with domains/proxy keys")
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
		*d = Domains(dc.Domains)
		return nil
	}
	return fmt.Errorf("domains entry must be a string, list, or object with domains/proxy")
}

// Parse parses a YAML config.
// Performs a two-pass parse: first into Config (Domains as []string),
// then a raw pass to extract proxy flags from structured domain entries.
func Parse(data []byte) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	// Second pass: extract proxy flags from raw YAML.
	// Domains unmarshal loses the proxy field (it only keeps the domain list).
	// Re-parse the domains section as raw nodes to find structured entries.
	var raw struct {
		Domains map[string]yaml.Node `yaml:"domains"`
	}
	if err := yaml.Unmarshal(data, &raw); err == nil {
		for svc, node := range raw.Domains {
			if node.Kind == yaml.MappingNode {
				var dc DomainConfig
				if err := node.Decode(&dc); err == nil && dc.Proxy {
					if cfg.DomainProxy == nil {
						cfg.DomainProxy = map[string]bool{}
					}
					cfg.DomainProxy[svc] = true
				}
			}
		}
	}

	return &cfg, nil
}

// ParseJSON parses a JSON config, including proxy flag extraction.
func ParseJSON(data []byte) (*Config, error) {
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	// Extract proxy flags from raw JSON (Domains unmarshal loses them).
	var raw struct {
		Domains map[string]json.RawMessage `json:"domains"`
	}
	if err := json.Unmarshal(data, &raw); err == nil {
		for svc, rawDomain := range raw.Domains {
			var dc DomainConfig
			if err := json.Unmarshal(rawDomain, &dc); err == nil && dc.Proxy {
				if cfg.DomainProxy == nil {
					cfg.DomainProxy = map[string]bool{}
				}
				cfg.DomainProxy[svc] = true
			}
		}
	}

	return &cfg, nil
}

// ParseEnv parses a .env file into a key-value map.
// Delegates to godotenv — handles quoting, escaping, multiline, comments.
func ParseEnv(data string) map[string]string {
	env, err := godotenv.Parse(strings.NewReader(data))
	if err != nil {
		return map[string]string{}
	}
	return env
}
