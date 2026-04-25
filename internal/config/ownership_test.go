package config

import (
	"testing"
)

// TestBuildOwnershipContext locks the cfg → expected-name mapping —
// the only place naming rules and cfg shape both live.
func TestBuildOwnershipContext(t *testing.T) {
	cfg := &AppConfig{
		App: "myapp",
		Env: "prod",
		Providers: ProvidersDef{
			Infra:  "hetzner",
			Tunnel: "cloudflare",
		},
		Servers: map[string]ServerDef{
			"master":  {Type: "cax11", Region: "nbg1", Role: "master"},
			"build-1": {Type: "cax21", Region: "nbg1", Role: "builder"},
		},
		Volumes: map[string]VolumeDef{
			"pgdata": {Size: 20, Server: "master"},
		},
		Storage: map[string]StorageDef{
			"assets": {},
		},
		Databases: map[string]DatabaseDef{
			"main":  {Engine: "postgres", Server: "master", Size: 20, Backup: &DatabaseBackupDef{Schedule: "0 3 * * *", Retention: 14}},
			"cache": {Engine: "postgres", Server: "master", Size: 5},
		},
		Domains: map[string][]string{
			"api": {"api.example.com", "api-alt.example.com"},
		},
	}

	ctx := BuildOwnershipContext(cfg)
	if ctx == nil {
		t.Fatal("nil context for valid cfg")
	}
	for _, want := range []string{"nvoi-myapp-prod-master", "nvoi-myapp-prod-build-1"} {
		if !ctx.ExpectedServers[want] {
			t.Errorf("ExpectedServers missing %q", want)
		}
	}
	if !ctx.ExpectedFirewalls["nvoi-myapp-prod-master-fw"] || !ctx.ExpectedFirewalls["nvoi-myapp-prod-builder-fw"] {
		t.Error("missing role firewalls")
	}
	if ctx.ExpectedFirewalls["nvoi-myapp-prod-worker-fw"] {
		t.Error("worker-fw should NOT be expected — no workers in cfg")
	}
	if !ctx.ExpectedNetworks["nvoi-myapp-prod-net"] {
		t.Error("network missing")
	}
	if !ctx.ExpectedVolumes["nvoi-myapp-prod-pgdata"] {
		t.Error("user volume missing")
	}
	if !ctx.ExpectedVolumes["nvoi-myapp-prod-build-1-builder-cache"] {
		t.Error("builder cache volume missing")
	}
	for _, want := range []string{"api.example.com", "api-alt.example.com"} {
		if !ctx.ExpectedDNS[want] {
			t.Errorf("ExpectedDNS missing %q", want)
		}
	}
	if !ctx.ExpectedBuckets["nvoi-myapp-prod-assets"] {
		t.Error("storage:assets bucket missing")
	}
	if !ctx.ExpectedBuckets["nvoi-myapp-prod-db-main-backups"] {
		t.Error("databases.main.backup bucket missing")
	}
	if ctx.ExpectedBuckets["nvoi-myapp-prod-db-cache-backups"] {
		t.Error("cache backup bucket should NOT be expected — backup unset")
	}
	if !ctx.ExpectedTunnels["nvoi-myapp-prod"] {
		t.Error("tunnel missing — providers.tunnel is set")
	}
}

func TestBuildOwnershipContext_NilCfg(t *testing.T) {
	if ctx := BuildOwnershipContext(nil); ctx != nil {
		t.Errorf("nil cfg should return nil, got %+v", ctx)
	}
}

func TestBuildOwnershipContext_NoTunnel(t *testing.T) {
	cfg := &AppConfig{
		App: "myapp", Env: "prod",
		Providers: ProvidersDef{Infra: "hetzner"},
		Servers:   map[string]ServerDef{"master": {Type: "cax11", Region: "nbg1", Role: "master"}},
	}
	ctx := BuildOwnershipContext(cfg)
	if len(ctx.ExpectedTunnels) != 0 {
		t.Errorf("ExpectedTunnels should be empty when providers.tunnel unset, got %v", ctx.ExpectedTunnels)
	}
}
