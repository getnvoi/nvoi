// Package config defines the public config schema for nvoi repos.
//
// A config is a YAML document that declaratively describes everything needed
// to deploy an application: servers, volumes, builds, storage, services, and
// domains. The orchestrator reads a config and executes the corresponding
// pkg/core/ functions in order — same sequence as bin/deploy-* scripts.
package config

import (
	"encoding/json"
	"strings"

	"github.com/joho/godotenv"
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
	Managed  string   `json:"managed,omitempty" yaml:"managed,omitempty"`   // managed service kind (postgres, redis, meilisearch)
	Port     int      `json:"port,omitempty" yaml:"port,omitempty"`         // exposed port
	Replicas int      `json:"replicas,omitempty" yaml:"replicas,omitempty"` // default 1
	Command  string   `json:"command,omitempty" yaml:"command,omitempty"`   // override entrypoint
	Health   string   `json:"health,omitempty" yaml:"health,omitempty"`     // readiness probe path
	Server   string   `json:"server,omitempty" yaml:"server,omitempty"`     // pin to server via node selector
	Volumes  []string `json:"volumes,omitempty" yaml:"volumes,omitempty"`   // name:/path
	Env      []string `json:"env,omitempty" yaml:"env,omitempty"`           // KEY=VALUE or KEY (resolved from .env)
	Secrets  []string `json:"secrets,omitempty" yaml:"secrets,omitempty"`   // k8s secret key refs (resolved from .env)
	Storage []string `json:"storage,omitempty" yaml:"storage,omitempty"` // storage name refs → STORAGE_{NAME}_*
	Uses    []string `json:"uses,omitempty" yaml:"uses,omitempty"`       // managed service refs → credentials injected as secrets
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
// Delegates to godotenv — handles quoting, escaping, multiline, comments.
func ParseEnv(data string) map[string]string {
	env, err := godotenv.Parse(strings.NewReader(data))
	if err != nil {
		return map[string]string{}
	}
	return env
}
