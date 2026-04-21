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
	Tunnel  app.ProviderRef
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
	App       string                 `yaml:"app"`
	Env       string                 `yaml:"env"`
	Providers ProvidersDef           `yaml:"providers"`
	Servers   map[string]ServerDef   `yaml:"servers,omitempty"`
	Firewall  []string               `yaml:"-"`
	Volumes   map[string]VolumeDef   `yaml:"volumes,omitempty"`
	Secrets   []string               `yaml:"secrets,omitempty"`
	Storage   map[string]StorageDef  `yaml:"storage,omitempty"`
	Registry  map[string]RegistryDef `yaml:"registry,omitempty"`
	Services  map[string]ServiceDef  `yaml:"services"`
	Crons     map[string]CronDef     `yaml:"crons,omitempty"`
	Domains   map[string][]string    `yaml:"domains,omitempty"`
	ACMEEmail string                 `yaml:"acme_email,omitempty"`

	// Sandbox is the forward-compat stub for InfraProvider implementations
	// that consume a "sandbox:" YAML block (Daytona via #48, future
	// sandbox-style backends). IaaS providers ignore it. The current type
	// is intentionally a placeholder — #48 fills in the real shape when
	// daytona/infra.go lands.
	Sandbox *SandboxBlock `yaml:"sandbox,omitempty"`

	// Resolved firewall names — populated by Resolve()
	MasterFirewall string `yaml:"-"`
	WorkerFirewall string `yaml:"-"`
}

// SandboxBlock is the placeholder for the "sandbox:" YAML block #48
// (Daytona) consumes. Defined here so the validator's ConsumesBlocks
// gating compiles; #48 fills in the real fields (snapshot, region,
// auto-stop, …). IaaS providers reject `sandbox:` via ConsumesBlocks.
type SandboxBlock struct {
	Snapshot string `yaml:"snapshot,omitempty"`
	Region   string `yaml:"region,omitempty"`
	// Future: AutoStopMinutes int, etc. — added by #48.
}

// RegistryDef holds credentials for a single private container registry.
// Username and Password may be literal values or `$VAR` references resolved
// at deploy time from the CredentialSource — same shape as `secrets:`.
//
// Example:
//
//	registry:
//	  ghcr.io:
//	    username: $GITHUB_USERNAME
//	    password: $GITHUB_TOKEN
type RegistryDef struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
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
	// Infra is the infra provider key (refactor #47). The legacy
	// `providers.compute` field was removed in C8 — configs using it
	// hit an unknown-field unmarshal error with a pointer to this rename.
	Infra string `yaml:"infra"`
	DNS   string `yaml:"dns,omitempty"`
	// Build selects the BuildProvider — the substrate `nvoi deploy`
	// physically runs on. Unset defaults to "local" (in-process reconcile
	// on the operator's machine). Future values: "ssh" (PR-B: remote on a
	// role: builder server), "daytona" (sandbox).
	//
	// Scalar only — same shape as Infra/DNS/Storage/Tunnel.
	Build   string      `yaml:"build,omitempty"`
	Storage string      `yaml:"storage,omitempty"`
	Secrets *SecretsDef `yaml:"secrets,omitempty"`
	Tunnel  string      `yaml:"tunnel,omitempty"`
}

// SecretsDef selects a secrets backend. Unset → nvoi falls back to
// `EnvSource` (os.Getenv) for every credential lookup, same as today.
// Set → the named backend's own creds are bootstrapped from env, then
// every downstream credential (compute / DNS / storage / SSH key /
// service $VAR) is fetched through the backend at deploy time. Strict
// mode — no env fallback once a backend is declared.
//
// Two YAML shapes, mirroring the scalar-or-struct pattern `build:` uses:
//
//	providers:
//	  secrets: doppler          # scalar — matches compute/dns/storage
//
//	providers:                  # struct — for future per-backend knobs
//	  secrets:
//	    kind: doppler
//	    # ttl: 1h, scopes: [...]   (none of these exist yet)
//
// Supported kinds: doppler | awssm | infisical.
type SecretsDef struct {
	Kind string `yaml:"kind"`
}

// UnmarshalYAML accepts both the scalar shortcut (`secrets: doppler`)
// and the struct form (`secrets: {kind: doppler}`). The scalar is
// preferred — same shape as the other providers — and the struct
// stays open for future per-backend fields without a breaking change.
func (s *SecretsDef) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		s.Kind = value.Value
		return nil
	case yaml.MappingNode:
		// Avoid an infinite UnmarshalYAML loop by decoding into a type
		// alias that doesn't re-implement UnmarshalYAML.
		type plain SecretsDef
		return value.Decode((*plain)(s))
	}
	return fmt.Errorf("providers.secrets: unexpected YAML kind — use `secrets: <name>` or `secrets: {kind: <name>}`")
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
	Image     string     `yaml:"image,omitempty"`
	Port      int        `yaml:"port,omitempty"`
	Replicas  int        `yaml:"replicas,omitempty"`
	Command   string     `yaml:"command,omitempty"`
	Health    string     `yaml:"health,omitempty"`
	Server    string     `yaml:"server,omitempty"`
	Servers   []string   `yaml:"servers,omitempty"`
	Env       []string   `yaml:"env,omitempty"`
	Secrets   []string   `yaml:"secrets,omitempty"`
	Storage   []string   `yaml:"storage,omitempty"`
	Volumes   []string   `yaml:"volumes,omitempty"`
	DependsOn []string   `yaml:"depends_on,omitempty"` // other services that must be Ready before this one is applied
	Build     *BuildSpec `yaml:"build,omitempty"`      // nil → pull-only; non-nil → build locally + push + pull
}

// BuildSpec declares that a service is built locally from a Dockerfile and
// pushed to the registry whose creds live in cfg.Registry before the
// cluster pulls it. Three YAML shapes, all equivalent in expressiveness
// to docker-compose's `build:` field:
//
//	build: true
//	  # context=./, dockerfile=./Dockerfile
//
//	build: services/api
//	  # context=services/api, dockerfile=services/api/Dockerfile
//
//	build:
//	  context: ./
//	  dockerfile: ./cmd/api/Dockerfile
//	  # monorepo pattern — Dockerfile lives in a subdir but the build
//	  # context covers the whole repo (e.g. Go builds that COPY go.mod
//	  # from the root).
//
// Context is what `docker build` sends as the build context (every file
// in it is COPY-able). Dockerfile is the path to the Dockerfile, which
// can live inside OR outside the context directory — Docker resolves
// `-f` independently of the context. When Dockerfile is empty, it
// defaults to `<Context>/Dockerfile` inside pkg/core/build.go.
type BuildSpec struct {
	Context    string `yaml:"context,omitempty"`
	Dockerfile string `yaml:"dockerfile,omitempty"`
}

// UnmarshalYAML accepts all three shapes. `build: false` sets Context to
// empty — the outer pointer stays non-nil but downstream code treats an
// empty Context as "no build".
func (b *BuildSpec) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		switch strings.ToLower(value.Value) {
		case "true":
			b.Context = "./"
			return nil
		case "false":
			b.Context = ""
			return nil
		default:
			b.Context = value.Value
			return nil
		}
	case yaml.MappingNode:
		// Decode through a type alias so we don't recurse back into
		// this custom unmarshaler.
		type plain BuildSpec
		if err := value.Decode((*plain)(b)); err != nil {
			return fmt.Errorf("build: %w", err)
		}
		// If only dockerfile was given, context defaults to the current
		// directory — matches docker-compose's behavior and keeps the
		// common monorepo case concise.
		if b.Context == "" && b.Dockerfile != "" {
			b.Context = "./"
		}
		return nil
	}
	return fmt.Errorf("build: unexpected YAML kind — use `build: true`, `build: <path>`, or `build: {context, dockerfile}`")
}

// BuildContext returns the resolved context directory. Callers treat the
// empty string as "no build".
func (b *BuildSpec) BuildContext() string {
	if b == nil {
		return ""
	}
	return b.Context
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
