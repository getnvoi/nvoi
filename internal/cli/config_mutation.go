package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/internal/api/config"
	"github.com/getnvoi/nvoi/internal/commands"
	"github.com/joho/godotenv"
	sigsyaml "sigs.k8s.io/yaml"
)

// ── Config load/push helpers ────────────────────────────────────────────────

func (c *CloudBackend) loadConfig() (*config.Config, map[string]string, int, error) {
	var resp struct {
		Config  string `json:"config"`
		Env     string `json:"env"`
		Version int    `json:"version"`
	}
	err := c.client.Do("GET", c.repoPath("/config")+"?reveal=true", nil, &resp)
	if err != nil {
		if IsNotFound(err) {
			return emptyConfig(), map[string]string{}, 0, nil
		}
		return nil, nil, 0, err
	}
	cfg, parseErr := config.Parse([]byte(resp.Config))
	if parseErr != nil {
		return nil, nil, 0, fmt.Errorf("parse config: %w", parseErr)
	}
	env, err := parseEnv(resp.Env)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("parse env: %w", err)
	}
	return cfg, env, resp.Version, nil
}

func emptyConfig() *config.Config {
	return &config.Config{
		Servers:  map[string]config.Server{},
		Services: map[string]config.Service{},
	}
}

type pushConfigBody struct {
	Config      string `json:"config"`
	Env         string `json:"env,omitempty"`
	BaseVersion int    `json:"base_version,omitempty"` // optimistic lock — reject if stale
}

func (c *CloudBackend) pushConfig(cfg *config.Config, env map[string]string, baseVersion int) error {
	if errs := config.Validate(cfg); len(errs) > 0 {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		return fmt.Errorf("invalid config:\n  %s", strings.Join(msgs, "\n  "))
	}

	yamlBytes, err := sigsyaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	envStr, err := serializeEnv(env)
	if err != nil {
		return fmt.Errorf("serialize env: %w", err)
	}
	body := pushConfigBody{
		Config:      string(yamlBytes),
		Env:         envStr,
		BaseVersion: baseVersion,
	}
	return c.client.Do("POST", c.repoPath("/config"), body, nil)
}

// ── Config-mutation methods ─────────────────────────────────────────────────

func (c *CloudBackend) InstanceSet(_ context.Context, name, serverType, region, role string) error {
	cfg, env, ver, err := c.loadConfig()
	if err != nil {
		return err
	}
	if cfg.Servers == nil {
		cfg.Servers = map[string]config.Server{}
	}
	cfg.Servers[name] = config.Server{Type: serverType, Region: region, Role: role}
	return c.pushConfig(cfg, env, ver)
}

func (c *CloudBackend) InstanceDelete(_ context.Context, name string) error {
	cfg, env, ver, err := c.loadConfig()
	if err != nil {
		return err
	}
	delete(cfg.Servers, name)
	return c.pushConfig(cfg, env, ver)
}

func (c *CloudBackend) FirewallSet(_ context.Context, args []string) error {
	cfg, env, ver, err := c.loadConfig()
	if err != nil {
		return err
	}
	fw := &config.FirewallConfig{}
	for _, arg := range args {
		if strings.Contains(arg, ":") {
			// Raw rule: port:cidr,cidr
			port, cidrs, _ := strings.Cut(arg, ":")
			if fw.Rules == nil {
				fw.Rules = map[string][]string{}
			}
			fw.Rules[port] = strings.Split(cidrs, ",")
		} else {
			fw.Preset = arg
		}
	}
	cfg.Firewall = fw
	return c.pushConfig(cfg, env, ver)
}

func (c *CloudBackend) VolumeSet(_ context.Context, name string, size int, server string) error {
	cfg, env, ver, err := c.loadConfig()
	if err != nil {
		return err
	}
	if cfg.Volumes == nil {
		cfg.Volumes = map[string]config.Volume{}
	}
	cfg.Volumes[name] = config.Volume{Size: size, Server: server}
	return c.pushConfig(cfg, env, ver)
}

func (c *CloudBackend) VolumeDelete(_ context.Context, name string) error {
	cfg, env, ver, err := c.loadConfig()
	if err != nil {
		return err
	}
	delete(cfg.Volumes, name)
	return c.pushConfig(cfg, env, ver)
}

func (c *CloudBackend) StorageSet(_ context.Context, name, bucket string, cors bool, expireDays int) error {
	cfg, env, ver, err := c.loadConfig()
	if err != nil {
		return err
	}
	if cfg.Storage == nil {
		cfg.Storage = map[string]config.Storage{}
	}
	cfg.Storage[name] = config.Storage{CORS: cors, ExpireDays: expireDays, Bucket: bucket}
	return c.pushConfig(cfg, env, ver)
}

func (c *CloudBackend) StorageDelete(_ context.Context, name string) error {
	cfg, env, ver, err := c.loadConfig()
	if err != nil {
		return err
	}
	delete(cfg.Storage, name)
	return c.pushConfig(cfg, env, ver)
}

func (c *CloudBackend) ServiceSet(_ context.Context, name string, opts commands.ServiceOpts) error {
	cfg, env, ver, err := c.loadConfig()
	if err != nil {
		return err
	}
	if cfg.Services == nil {
		cfg.Services = map[string]config.Service{}
	}
	// Merge over existing entry to preserve fields set by other commands (e.g. Uses).
	svc := cfg.Services[name]
	svc.Image = opts.Image
	svc.Port = opts.Port
	svc.Replicas = opts.Replicas
	svc.Command = opts.Command
	svc.Server = opts.Server
	svc.Health = opts.Health
	svc.Env = opts.Env
	svc.Secrets = opts.Secrets
	svc.Storage = opts.Storage
	svc.Volumes = opts.Volumes
	cfg.Services[name] = svc
	return c.pushConfig(cfg, env, ver)
}

func (c *CloudBackend) ServiceDelete(_ context.Context, name string) error {
	cfg, env, ver, err := c.loadConfig()
	if err != nil {
		return err
	}
	delete(cfg.Services, name)
	return c.pushConfig(cfg, env, ver)
}

func (c *CloudBackend) CronSet(_ context.Context, name string, opts commands.CronOpts) error {
	cfg, env, ver, err := c.loadConfig()
	if err != nil {
		return err
	}
	if cfg.Crons == nil {
		cfg.Crons = map[string]config.Cron{}
	}
	cfg.Crons[name] = config.Cron{
		Workload: config.Workload{
			Image:   opts.Image,
			Command: opts.Command,
			Server:  opts.Server,
			Env:     opts.Env,
			Secrets: opts.Secrets,
			Storage: opts.Storage,
			Volumes: opts.Volumes,
		},
		Schedule: opts.Schedule,
	}
	return c.pushConfig(cfg, env, ver)
}

func (c *CloudBackend) CronDelete(_ context.Context, name string) error {
	cfg, env, ver, err := c.loadConfig()
	if err != nil {
		return err
	}
	delete(cfg.Crons, name)
	return c.pushConfig(cfg, env, ver)
}

func (c *CloudBackend) DatabaseSet(_ context.Context, name string, opts commands.ManagedOpts) error {
	cfg, env, ver, err := c.loadConfig()
	if err != nil {
		return err
	}
	if cfg.Services == nil {
		cfg.Services = map[string]config.Service{}
	}
	cfg.Services[name] = config.Service{
		Workload:      config.Workload{Image: opts.Image, Secrets: opts.Secrets},
		Managed:       opts.Kind,
		VolumeSize:    opts.VolumeSize,
		BackupStorage: opts.BackupStorage,
		BackupCron:    opts.BackupCron,
	}
	return c.pushConfig(cfg, env, ver)
}

func (c *CloudBackend) DatabaseDelete(_ context.Context, name, _ string) error {
	cfg, env, ver, err := c.loadConfig()
	if err != nil {
		return err
	}
	delete(cfg.Services, name)
	return c.pushConfig(cfg, env, ver)
}

func (c *CloudBackend) AgentSet(_ context.Context, name string, opts commands.ManagedOpts) error {
	cfg, env, ver, err := c.loadConfig()
	if err != nil {
		return err
	}
	if cfg.Services == nil {
		cfg.Services = map[string]config.Service{}
	}
	cfg.Services[name] = config.Service{
		Workload: config.Workload{Secrets: opts.Secrets},
		Managed:  opts.Kind,
	}
	return c.pushConfig(cfg, env, ver)
}

func (c *CloudBackend) AgentDelete(_ context.Context, name, _ string) error {
	cfg, env, ver, err := c.loadConfig()
	if err != nil {
		return err
	}
	delete(cfg.Services, name)
	return c.pushConfig(cfg, env, ver)
}

func (c *CloudBackend) SecretSet(_ context.Context, key, value string) error {
	cfg, env, ver, err := c.loadConfig()
	if err != nil {
		return err
	}
	env[key] = value
	return c.pushConfig(cfg, env, ver)
}

func (c *CloudBackend) SecretDelete(_ context.Context, key string) error {
	cfg, env, ver, err := c.loadConfig()
	if err != nil {
		return err
	}
	delete(env, key)
	return c.pushConfig(cfg, env, ver)
}

// ── env helpers ─────────────────────────────────────────────────────────────
// The API stores secrets as dotenv-formatted text. We use godotenv for
// parsing and serialization so multiline values (PEM certs, JWT keys)
// survive the round-trip — godotenv quotes them correctly.

func parseEnv(env string) (map[string]string, error) {
	if env == "" {
		return map[string]string{}, nil
	}
	return godotenv.Parse(strings.NewReader(env))
}

func serializeEnv(m map[string]string) (string, error) {
	return godotenv.Marshal(m)
}

func (c *CloudBackend) DNSSet(_ context.Context, routes []commands.RouteArg, cloudflareManaged bool) error {
	cfg, env, ver, err := c.loadConfig()
	if err != nil {
		return err
	}
	if cfg.Domains == nil {
		cfg.Domains = map[string]config.Domains{}
	}
	for _, route := range routes {
		cfg.Domains[route.Service] = config.Domains(route.Domains)
	}
	// --cloudflare-managed on dns set means proxied DNS records.
	// Set the ingress flag so the planner emits cloudflare_managed on dns.set steps.
	// Don't overwrite existing cert/key ingress — they're mutually exclusive.
	if cloudflareManaged {
		if cfg.Ingress == nil {
			cfg.Ingress = &config.IngressConfig{CloudflareManaged: true}
		} else if cfg.Ingress.Cert == "" {
			cfg.Ingress.CloudflareManaged = true
		}
	}
	return c.pushConfig(cfg, env, ver)
}

func (c *CloudBackend) DNSDelete(_ context.Context, routes []commands.RouteArg) error {
	cfg, env, ver, err := c.loadConfig()
	if err != nil {
		return err
	}
	for _, route := range routes {
		delete(cfg.Domains, route.Service)
	}
	return c.pushConfig(cfg, env, ver)
}

func (c *CloudBackend) IngressSet(_ context.Context, routes []commands.RouteArg, cloudflareManaged bool, cert, key string) error {
	cfg, env, ver, err := c.loadConfig()
	if err != nil {
		return err
	}
	if cfg.Domains == nil {
		cfg.Domains = map[string]config.Domains{}
	}
	for _, route := range routes {
		cfg.Domains[route.Service] = config.Domains(route.Domains)
	}
	if cloudflareManaged {
		cfg.Ingress = &config.IngressConfig{CloudflareManaged: true}
	} else if cert != "" && key != "" {
		env["TLS_CERT_PEM"] = cert
		env["TLS_KEY_PEM"] = key
		cfg.Ingress = &config.IngressConfig{Cert: "TLS_CERT_PEM", Key: "TLS_KEY_PEM"}
	}
	return c.pushConfig(cfg, env, ver)
}

func (c *CloudBackend) IngressDelete(_ context.Context, routes []commands.RouteArg, cloudflareManaged bool) error {
	cfg, env, ver, err := c.loadConfig()
	if err != nil {
		return err
	}
	for _, route := range routes {
		delete(cfg.Domains, route.Service)
	}
	if len(cfg.Domains) == 0 {
		cfg.Ingress = nil
	} else if cloudflareManaged && cfg.Ingress != nil {
		// Preserve cloudflare-managed flag when routes remain —
		// executor needs it for Origin CA cert revocation on the deleted routes.
		cfg.Ingress.CloudflareManaged = true
	}
	return c.pushConfig(cfg, env, ver)
}

func (c *CloudBackend) Build(_ context.Context, opts commands.BuildOpts) error {
	cfg, env, ver, err := c.loadConfig()
	if err != nil {
		return err
	}
	if cfg.Build == nil {
		cfg.Build = map[string]config.Build{}
	}
	for _, target := range opts.Targets {
		name, source, ok := strings.Cut(target, ":")
		if !ok {
			return fmt.Errorf("invalid build target %q — expected name:source", target)
		}
		cfg.Build[name] = config.Build{Source: source}
	}
	return c.pushConfig(cfg, env, ver)
}
