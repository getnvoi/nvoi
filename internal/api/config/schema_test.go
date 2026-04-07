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
  worker-1:
    type: cx33
    region: fsn1

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
    build: web
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
	if web.Build != "web" {
		t.Errorf("web build = %q", web.Build)
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

// ── Firewall unmarshaling ─────────────────────────────────────────────────────

func TestFirewall_YAMLPresetString(t *testing.T) {
	input := `
servers:
  master:
    type: cx23
    region: fsn1
services:
  web:
    image: nginx
    port: 80
firewall: default
domains:
  web: example.com
`
	cfg, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Firewall == nil {
		t.Fatal("firewall should not be nil")
	}
	if cfg.Firewall.Preset != "default" {
		t.Errorf("preset = %q, want default", cfg.Firewall.Preset)
	}
	if cfg.Firewall.Rules != nil {
		t.Errorf("rules should be nil for preset-only, got %v", cfg.Firewall.Rules)
	}
}

func TestFirewall_YAMLPresetPlusRules(t *testing.T) {
	input := `
servers:
  master:
    type: cx23
    region: fsn1
services:
  web:
    image: nginx
    port: 80
firewall:
  preset: cloudflare
  "443":
    - 0.0.0.0/0
domains:
  web:
    domains: [example.com]
    proxy: true
`
	cfg, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Firewall.Preset != "cloudflare" {
		t.Errorf("preset = %q, want cloudflare", cfg.Firewall.Preset)
	}
	if len(cfg.Firewall.Rules["443"]) != 1 || cfg.Firewall.Rules["443"][0] != "0.0.0.0/0" {
		t.Errorf("rules[443] = %v, want [0.0.0.0/0]", cfg.Firewall.Rules["443"])
	}
}

func TestFirewall_YAMLExplicitRulesOnly(t *testing.T) {
	input := `
servers:
  master:
    type: cx23
    region: fsn1
services:
  web:
    image: nginx
    port: 80
firewall:
  "80":
    - 0.0.0.0/0
  "443":
    - 0.0.0.0/0
domains:
  web: example.com
`
	cfg, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Firewall.Preset != "" {
		t.Errorf("preset should be empty, got %q", cfg.Firewall.Preset)
	}
	if len(cfg.Firewall.Rules) != 2 {
		t.Errorf("rules should have 2 ports, got %d", len(cfg.Firewall.Rules))
	}
}

func TestFirewall_JSONPresetString(t *testing.T) {
	var fw FirewallConfig
	if err := json.Unmarshal([]byte(`"default"`), &fw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if fw.Preset != "default" {
		t.Errorf("preset = %q, want default", fw.Preset)
	}
}

func TestFirewall_JSONPresetPlusRules(t *testing.T) {
	var fw FirewallConfig
	if err := json.Unmarshal([]byte(`{"preset":"cloudflare","443":["0.0.0.0/0"]}`), &fw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if fw.Preset != "cloudflare" {
		t.Errorf("preset = %q, want cloudflare", fw.Preset)
	}
	if len(fw.Rules["443"]) != 1 {
		t.Errorf("rules[443] = %v", fw.Rules["443"])
	}
}

// ── Domains with proxy ───────────────────────────────────────────────────────

func TestDomains_YAMLStructuredWithProxy(t *testing.T) {
	input := `
servers:
  master:
    type: cx23
    region: fsn1
services:
  web:
    image: nginx
    port: 80
firewall: cloudflare
domains:
  web:
    domains: [example.com, www.example.com]
    proxy: true
`
	cfg, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(cfg.Domains["web"]) != 2 {
		t.Errorf("domains = %v, want 2 entries", cfg.Domains["web"])
	}
	if !cfg.DomainProxy["web"] {
		t.Error("DomainProxy[web] should be true")
	}
}

func TestDomains_YAMLStructuredWithoutProxy(t *testing.T) {
	input := `
servers:
  master:
    type: cx23
    region: fsn1
services:
  web:
    image: nginx
    port: 80
firewall: default
domains:
  web:
    domains: [example.com]
`
	cfg, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.DomainProxy["web"] {
		t.Error("DomainProxy[web] should be false when proxy not set")
	}
}

func TestDomains_JSONStructuredWithProxy(t *testing.T) {
	input := `{
		"servers": {"master": {"type": "cx23", "region": "fsn1"}},
		"services": {"web": {"image": "nginx", "port": 80}},
		"firewall": "cloudflare",
		"domains": {"web": {"domains": ["example.com"], "proxy": true}}
	}`
	cfg, err := ParseJSON([]byte(input))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(cfg.Domains["web"]) != 1 {
		t.Errorf("domains = %v", cfg.Domains["web"])
	}
	if !cfg.DomainProxy["web"] {
		t.Error("DomainProxy[web] should be true")
	}
}

// ── DomainProxy round-trip ────────────────────────────────────────────────────

func TestDomainProxy_JSONRoundTrip(t *testing.T) {
	original := &Config{
		Servers:  map[string]Server{"master": {Type: "cx23", Region: "fsn1"}},
		Firewall: &FirewallConfig{Preset: "cloudflare"},
		Services: map[string]Service{"web": {Image: "nginx", Port: 80}},
		Domains:  map[string]Domains{"web": {"example.com", "www.example.com"}},
		DomainProxy: map[string]bool{"web": true},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	restored, err := ParseJSON(data)
	if err != nil {
		t.Fatalf("parseJSON: %v", err)
	}

	if !restored.DomainProxy["web"] {
		t.Error("DomainProxy[web] lost after JSON round-trip")
	}
	if len(restored.Domains["web"]) != 2 {
		t.Errorf("domains lost after round-trip: %v", restored.Domains["web"])
	}
}

func TestFirewall_JSONRoundTrip(t *testing.T) {
	original := &FirewallConfig{Preset: "default"}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Preset-only should serialize as string "default"
	if string(data) != `"default"` {
		t.Errorf("preset-only should marshal as string, got %s", string(data))
	}

	var restored FirewallConfig
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if restored.Preset != "default" {
		t.Errorf("preset = %q after round-trip", restored.Preset)
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
	env := ParseEnv(input)

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
	env := ParseEnv("")
	if len(env) != 0 {
		t.Errorf("expected empty map, got %v", env)
	}
}
