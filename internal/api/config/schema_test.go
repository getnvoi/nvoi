package config

import (
	"encoding/json"
	"testing"
)

// ── Parse ──────────────────────────────────────────────────────────────────────

func TestParse_FullConfig(t *testing.T) {
	yaml := `
servers:
  master:
    type: cx23
    region: fsn1
    role: master
  worker-1:
    type: cx33
    region: fsn1
    role: worker

volumes:
  pgdata:
    size: 30
    server: master

build:
  web:
    source: benbonnet/dummy-rails

storage:
  assets:
    cors: true
    expire_days: 90

services:
  db:
    image: postgres:17
    volumes:
      - pgdata:/var/lib/postgresql/data
    env:
      - POSTGRES_USER
      - POSTGRES_DB
    secrets:
      - POSTGRES_PASSWORD
  web:
    image: web
    port: 80
    replicas: 2
    health: /up
    server: worker-1
    env:
      - RAILS_ENV=production
      - POSTGRES_HOST=db
    secrets:
      - POSTGRES_PASSWORD
      - RAILS_MASTER_KEY
    storage:
      - assets

domains:
  web: final.nvoi.to
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if len(cfg.Servers) != 2 {
		t.Errorf("servers: got %d, want 2", len(cfg.Servers))
	}
	if cfg.Servers["master"].Type != "cx23" {
		t.Errorf("master type = %q", cfg.Servers["master"].Type)
	}
	if cfg.Servers["worker-1"].Region != "fsn1" {
		t.Errorf("worker-1 region = %q", cfg.Servers["worker-1"].Region)
	}
	if cfg.Volumes["pgdata"].Size != 30 {
		t.Errorf("pgdata size = %d", cfg.Volumes["pgdata"].Size)
	}
	if cfg.Build["web"].Source != "benbonnet/dummy-rails" {
		t.Errorf("build source = %q", cfg.Build["web"].Source)
	}
	if !cfg.Storage["assets"].CORS {
		t.Error("assets cors should be true")
	}
	if cfg.Storage["assets"].ExpireDays != 90 {
		t.Errorf("assets expire_days = %d", cfg.Storage["assets"].ExpireDays)
	}

	web := cfg.Services["web"]
	if web.Image != "web" {
		t.Errorf("web image = %q", web.Image)
	}
	if web.Port != 80 {
		t.Errorf("web port = %d", web.Port)
	}
	if web.Replicas != 2 {
		t.Errorf("web replicas = %d", web.Replicas)
	}
	if web.Server != "worker-1" {
		t.Errorf("web server = %q", web.Server)
	}
	if len(web.Storage) != 1 || web.Storage[0] != "assets" {
		t.Errorf("web storage = %v", web.Storage)
	}

	db := cfg.Services["db"]
	if db.Image != "postgres:17" {
		t.Errorf("db image = %q", db.Image)
	}
	if len(db.Volumes) != 1 || db.Volumes[0] != "pgdata:/var/lib/postgresql/data" {
		t.Errorf("db volumes = %v", db.Volumes)
	}

	if len(cfg.Domains["web"]) != 1 || cfg.Domains["web"][0] != "final.nvoi.to" {
		t.Errorf("domains web = %v", cfg.Domains["web"])
	}
}

func TestParse_MinimalConfig(t *testing.T) {
	yaml := `
servers:
  master:
    type: t3.medium
    region: eu-west-3
    role: master
services:
  web:
    image: nginx:latest
    port: 80
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(cfg.Servers) != 1 {
		t.Errorf("servers: got %d", len(cfg.Servers))
	}
	if len(cfg.Services) != 1 {
		t.Errorf("services: got %d", len(cfg.Services))
	}
}

// ── Domains unmarshaling ───────────────────────────────────────────────────────

func TestDomains_YAMLSingleString(t *testing.T) {
	yaml := `
servers:
  master:
    type: cx23
    region: fsn1
    role: master
services:
  web:
    image: nginx
    port: 80
domains:
  web: example.com
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(cfg.Domains["web"]) != 1 || cfg.Domains["web"][0] != "example.com" {
		t.Errorf("domains = %v, want [example.com]", cfg.Domains["web"])
	}
}

func TestDomains_YAMLList(t *testing.T) {
	yaml := `
servers:
  master:
    type: cx23
    region: fsn1
    role: master
services:
  web:
    image: nginx
    port: 80
domains:
  web: [example.com, www.example.com]
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(cfg.Domains["web"]) != 2 {
		t.Errorf("domains = %v, want 2 entries", cfg.Domains["web"])
	}
}

func TestDomains_JSONSingleString(t *testing.T) {
	var d Domains
	if err := json.Unmarshal([]byte(`"example.com"`), &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(d) != 1 || d[0] != "example.com" {
		t.Errorf("got %v", d)
	}
}

func TestDomains_JSONList(t *testing.T) {
	var d Domains
	if err := json.Unmarshal([]byte(`["a.com","b.com"]`), &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(d) != 2 {
		t.Errorf("got %v", d)
	}
}

func TestParse_IngressCloudflareManaged(t *testing.T) {
	yaml := `
servers:
  master:
    type: cx23
    region: fsn1
    role: master
services:
  web:
    image: nginx
    port: 80
domains:
  web: [example.com]
ingress:
  cloudflare-managed: true
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Ingress == nil {
		t.Fatal("expected ingress config")
	}
	if !cfg.Ingress.CloudflareManaged {
		t.Fatal("expected cloudflare-managed = true")
	}
}

func TestParse_LegacyDomainProxySyntaxRejected(t *testing.T) {
	yaml := `
servers:
  master:
    type: cx23
    region: fsn1
    role: master
services:
  web:
    image: nginx
    port: 80
domains:
  web:
    domains: [example.com]
    proxy: true
`
	if _, err := Parse([]byte(yaml)); err == nil {
		t.Fatal("expected legacy proxy syntax to fail")
	}
}

// ── Firewall ──────────────────────────────────────────────────────────────────

func TestParse_FirewallPresetString(t *testing.T) {
	cfg, err := Parse([]byte(`
servers:
  master: { type: cx23, region: fsn1, role: master }
services:
  web: { image: nginx }
firewall: cloudflare
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Firewall == nil || cfg.Firewall.Preset != "cloudflare" {
		t.Fatalf("firewall = %+v", cfg.Firewall)
	}
}

func TestParse_FirewallPresetPlusRules(t *testing.T) {
	cfg, err := Parse([]byte(`
servers:
  master: { type: cx23, region: fsn1, role: master }
services:
  web: { image: nginx }
firewall:
  preset: cloudflare
  443:
    - 0.0.0.0/0
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Firewall.Preset != "cloudflare" {
		t.Fatalf("preset = %q", cfg.Firewall.Preset)
	}
	cidrs, ok := cfg.Firewall.Rules["443"]
	if !ok || len(cidrs) != 1 || cidrs[0] != "0.0.0.0/0" {
		t.Fatalf("rules = %+v", cfg.Firewall.Rules)
	}
}

func TestParse_FirewallRawOnly(t *testing.T) {
	cfg, err := Parse([]byte(`
servers:
  master: { type: cx23, region: fsn1, role: master }
services:
  web: { image: nginx }
firewall:
  "80":
    - 0.0.0.0/0
  "443":
    - 10.0.0.0/8
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Firewall.Preset != "" {
		t.Fatalf("preset should be empty, got %q", cfg.Firewall.Preset)
	}
	if len(cfg.Firewall.Rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(cfg.Firewall.Rules))
	}
}

func TestFirewall_JSONRoundTrip(t *testing.T) {
	// Marshal via MarshalJSON, unmarshal via UnmarshalJSON — the path sigs.k8s.io/yaml uses.
	fw := FirewallConfig{Preset: "cloudflare", Rules: map[string][]string{"443": {"0.0.0.0/0"}}}
	data, err := json.Marshal(fw)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got FirewallConfig
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Preset != "cloudflare" {
		t.Fatalf("preset = %q", got.Preset)
	}
	if got.Rules["443"][0] != "0.0.0.0/0" {
		t.Fatalf("rules = %+v", got.Rules)
	}
}

// ── ParseEnv ───────────────────────────────────────────────────────────────────

func TestParseEnv(t *testing.T) {
	input := `
# Database
POSTGRES_USER=myapp
POSTGRES_PASSWORD="s3cret"
POSTGRES_DB='myapp_prod'
RAILS_MASTER_KEY=abc123

# Empty
EMPTY=
`
	env, err := ParseEnv(input)
	if err != nil {
		t.Fatalf("ParseEnv: %v", err)
	}

	tests := map[string]string{
		"POSTGRES_USER":     "myapp",
		"POSTGRES_PASSWORD": "s3cret",
		"POSTGRES_DB":       "myapp_prod",
		"RAILS_MASTER_KEY":  "abc123",
		"EMPTY":             "",
	}
	for k, want := range tests {
		got, ok := env[k]
		if !ok {
			t.Errorf("%s: not found", k)
			continue
		}
		if got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if _, ok := env["# Database"]; ok {
		t.Error("comment should not be parsed")
	}
}

func TestParseEnv_Empty(t *testing.T) {
	env, err := ParseEnv("")
	if err != nil {
		t.Fatalf("ParseEnv: %v", err)
	}
	if len(env) != 0 {
		t.Errorf("expected empty map, got %v", env)
	}
}
