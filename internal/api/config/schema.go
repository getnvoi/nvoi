// Package config defines the public config schema for nvoi repos.
//
// A config is a YAML document that declaratively describes everything needed
// to deploy an application: servers, volumes, builds, storage, services, and
// domains. The orchestrator reads a config and executes the corresponding
// pkg/core/ functions in order — same sequence as bin/deploy-* scripts.
package config

import (
	"encoding/json"

	"gopkg.in/yaml.v3"
)

// Config is the public schema — what users write.
type Config struct {
	Servers  map[string]Server  `json:"servers" yaml:"servers"`
	Volumes  map[string]Volume  `json:"volumes,omitempty" yaml:"volumes,omitempty"`
	Build    map[string]Build   `json:"build,omitempty" yaml:"build,omitempty"`
	Storage  map[string]Storage `json:"storage,omitempty" yaml:"storage,omitempty"`
	Services map[string]Service `json:"services" yaml:"services"`
	Domains  map[string]Domains `json:"domains,omitempty" yaml:"domains,omitempty"`
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
	Port     int      `json:"port,omitempty" yaml:"port,omitempty"`         // exposed port
	Replicas int      `json:"replicas,omitempty" yaml:"replicas,omitempty"` // default 1
	Command  string   `json:"command,omitempty" yaml:"command,omitempty"`   // override entrypoint
	Health   string   `json:"health,omitempty" yaml:"health,omitempty"`     // readiness probe path
	Server   string   `json:"server,omitempty" yaml:"server,omitempty"`     // pin to server via node selector
	Volumes  []string `json:"volumes,omitempty" yaml:"volumes,omitempty"`   // name:/path
	Env      []string `json:"env,omitempty" yaml:"env,omitempty"`           // KEY=VALUE or KEY (resolved from .env)
	Secrets  []string `json:"secrets,omitempty" yaml:"secrets,omitempty"`   // k8s secret key refs (resolved from .env)
	Storage  []string `json:"storage,omitempty" yaml:"storage,omitempty"`   // storage name refs → STORAGE_{NAME}_*
}

// Domains supports both a single string and a list of strings in YAML/JSON.
//
//	domains:
//	  web: example.com
//	  api: [example.com, api.example.com]
type Domains []string

func (d *Domains) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		*d = Domains{value.Value}
		return nil
	}
	var list []string
	if err := value.Decode(&list); err != nil {
		return err
	}
	*d = list
	return nil
}

func (d *Domains) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*d = Domains{s}
		return nil
	}
	var list []string
	if err := json.Unmarshal(data, &list); err != nil {
		return err
	}
	*d = list
	return nil
}

// Parse parses a YAML config.
func Parse(data []byte) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// ParseEnv parses a .env file into a key-value map.
// Supports KEY=VALUE, KEY="VALUE", KEY='VALUE', comments (#), empty lines.
func ParseEnv(data string) map[string]string {
	env := make(map[string]string)
	for _, line := range splitLines(data) {
		line = trimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		k, v, ok := cutString(line, "=")
		if !ok {
			continue
		}
		v = unquote(v)
		env[k] = v
	}
	return env
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func trimSpace(s string) string {
	i, j := 0, len(s)
	for i < j && (s[i] == ' ' || s[i] == '\t' || s[i] == '\r') {
		i++
	}
	for j > i && (s[j-1] == ' ' || s[j-1] == '\t' || s[j-1] == '\r') {
		j--
	}
	return s[i:j]
}

func cutString(s, sep string) (string, string, bool) {
	for i := 0; i+len(sep) <= len(s); i++ {
		if s[i:i+len(sep)] == sep {
			return s[:i], s[i+len(sep):], true
		}
	}
	return s, "", false
}

func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
