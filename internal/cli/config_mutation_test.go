package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/internal/api/config"
	"github.com/getnvoi/nvoi/internal/commands"
	sigsyaml "sigs.k8s.io/yaml"
)

// ── test helpers ──────────────────────────────────────────────────────────────

// configServer creates an httptest server that stores config+env in memory.
// GET  .../config returns the stored YAML + env.
// POST .../config parses the pushed YAML and updates stored state.
func configServer(t *testing.T, initial *config.Config, initialEnv string) *httptest.Server {
	t.Helper()
	// Store as YAML bytes.
	stored, _ := sigsyaml.Marshal(initial)
	storedEnv := initialEnv

	mux := http.NewServeMux()
	mux.HandleFunc("/workspaces/w/repos/r/config", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			json.NewEncoder(w).Encode(map[string]string{
				"config": string(stored),
				"env":    storedEnv,
			})
		case "POST":
			var body pushConfigBody
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			// Validate the YAML parses.
			if _, err := config.Parse([]byte(body.Config)); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			stored = []byte(body.Config)
			storedEnv = body.Env
			w.WriteHeader(200)
		default:
			http.Error(w, "method not allowed", 405)
		}
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func testBackend(t *testing.T, ts *httptest.Server) *CloudBackend {
	t.Helper()
	return &CloudBackend{
		client: &APIClient{base: ts.URL, http: ts.Client()},
		wsID:   "w",
		repoID: "r",
	}
}

// validCfg returns a config that passes Validate — has server, service with port,
// firewall opening 80/443 so domain operations work.
func validCfg() *config.Config {
	return &config.Config{
		Servers:  map[string]config.Server{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Firewall: &config.FirewallConfig{Preset: "default"},
		Services: map[string]config.Service{"web": {Workload: config.Workload{Image: "nginx"}, Port: 80}},
	}
}

func getCfg(t *testing.T, b *CloudBackend) *config.Config {
	t.Helper()
	cfg, _, _, err := b.loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	return cfg
}

func getEnv(t *testing.T, b *CloudBackend) map[string]string {
	t.Helper()
	_, env, _, err := b.loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	return env
}

// ── loadConfig ────────────────────────────────────────────────────────────────

func TestLoadConfig_404ReturnsEmpty(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"not found"}`, 404)
	}))
	defer ts.Close()

	b := testBackend(t, ts)
	cfg, env, _, err := b.loadConfig()
	if err != nil {
		t.Fatalf("expected nil error for 404, got: %v", err)
	}
	if cfg == nil || cfg.Servers == nil {
		t.Fatal("expected initialized empty config")
	}
	if len(env) != 0 {
		t.Fatalf("expected empty env, got: %v", env)
	}
}

func TestLoadConfig_500ReturnsError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", 500)
	}))
	defer ts.Close()

	b := testBackend(t, ts)
	_, _, _, err := b.loadConfig()
	if err == nil {
		t.Fatal("expected error for 500")
	}
}

// ── InstanceSet / InstanceDelete ──────────────────────────────────────────────

func TestInstanceSet_AddsServer(t *testing.T) {
	b := testBackend(t, configServer(t, validCfg(), ""))

	if err := b.InstanceSet(context.Background(), "worker-1", "cx33", "fsn1", "worker"); err != nil {
		t.Fatalf("InstanceSet: %v", err)
	}

	cfg := getCfg(t, b)
	srv, ok := cfg.Servers["worker-1"]
	if !ok {
		t.Fatal("worker-1 not in servers")
	}
	if srv.Type != "cx33" || srv.Region != "fsn1" {
		t.Fatalf("server = %+v", srv)
	}
	if _, ok := cfg.Servers["master"]; !ok {
		t.Fatal("master should still exist")
	}
}

func TestInstanceDelete_RemovesServer(t *testing.T) {
	b := testBackend(t, configServer(t, validCfg(), ""))

	if err := b.InstanceDelete(context.Background(), "master"); err != nil {
		t.Fatalf("InstanceDelete: %v", err)
	}

	cfg := getCfg(t, b)
	if _, ok := cfg.Servers["master"]; ok {
		t.Fatal("master should be deleted")
	}
}

// ── FirewallSet ───────────────────────────────────────────────────────────────

func TestFirewallSet_PresetOnly(t *testing.T) {
	b := testBackend(t, configServer(t, validCfg(), ""))

	if err := b.FirewallSet(context.Background(), []string{"default"}); err != nil {
		t.Fatalf("FirewallSet: %v", err)
	}

	cfg := getCfg(t, b)
	if cfg.Firewall == nil || cfg.Firewall.Preset != "default" {
		t.Fatalf("firewall = %+v", cfg.Firewall)
	}
}

func TestFirewallSet_PresetPlusRawRules(t *testing.T) {
	b := testBackend(t, configServer(t, validCfg(), ""))

	if err := b.FirewallSet(context.Background(), []string{"default", "443:10.0.0.0/8"}); err != nil {
		t.Fatalf("FirewallSet: %v", err)
	}

	cfg := getCfg(t, b)
	if cfg.Firewall.Preset != "default" {
		t.Fatalf("preset = %q", cfg.Firewall.Preset)
	}
	cidrs, ok := cfg.Firewall.Rules["443"]
	if !ok || len(cidrs) == 0 {
		t.Fatal("expected 443 rule override")
	}
	if cidrs[0] != "10.0.0.0/8" {
		t.Fatalf("443 cidrs = %v", cidrs)
	}
}

func TestFirewallSet_RawOnly(t *testing.T) {
	b := testBackend(t, configServer(t, validCfg(), ""))

	if err := b.FirewallSet(context.Background(), []string{"80:0.0.0.0/0", "443:0.0.0.0/0"}); err != nil {
		t.Fatalf("FirewallSet: %v", err)
	}

	cfg := getCfg(t, b)
	if cfg.Firewall.Preset != "" {
		t.Fatalf("preset should be empty, got %q", cfg.Firewall.Preset)
	}
	if len(cfg.Firewall.Rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(cfg.Firewall.Rules))
	}
}

// ── SecretSet / SecretDelete ──────────────────────────────────────────────────

func TestSecretSet_AppendsToEmptyEnv(t *testing.T) {
	b := testBackend(t, configServer(t, validCfg(), ""))

	if err := b.SecretSet(context.Background(), "KEY", "val"); err != nil {
		t.Fatalf("SecretSet: %v", err)
	}

	env := getEnv(t, b)
	if env["KEY"] != "val" {
		t.Fatalf("KEY = %q, want val", env["KEY"])
	}
}

func TestSecretSet_NoBlankLineAccumulation(t *testing.T) {
	b := testBackend(t, configServer(t, validCfg(), ""))

	b.SecretSet(context.Background(), "A", "1")
	b.SecretSet(context.Background(), "B", "2")
	b.SecretSet(context.Background(), "C", "3")

	env := getEnv(t, b)
	if len(env) != 3 {
		t.Fatalf("expected 3 keys, got %d: %v", len(env), env)
	}
}

func TestSecretSet_UpdatesExisting(t *testing.T) {
	b := testBackend(t, configServer(t, validCfg(), "KEY=old"))

	if err := b.SecretSet(context.Background(), "KEY", "new"); err != nil {
		t.Fatalf("SecretSet: %v", err)
	}

	env := getEnv(t, b)
	if env["KEY"] != "new" {
		t.Fatalf("KEY = %q, want new", env["KEY"])
	}
}

func TestSecretSet_PreservesOtherKeys(t *testing.T) {
	b := testBackend(t, configServer(t, validCfg(), "A=1\nB=2"))

	if err := b.SecretSet(context.Background(), "C", "3"); err != nil {
		t.Fatalf("SecretSet: %v", err)
	}

	env := getEnv(t, b)
	if env["A"] != "1" || env["B"] != "2" || env["C"] != "3" {
		t.Fatalf("env = %v", env)
	}
}

func TestSecretDelete_RemovesKey(t *testing.T) {
	b := testBackend(t, configServer(t, validCfg(), "A=1\nB=2\nC=3"))

	if err := b.SecretDelete(context.Background(), "B"); err != nil {
		t.Fatalf("SecretDelete: %v", err)
	}

	env := getEnv(t, b)
	if _, ok := env["B"]; ok {
		t.Fatal("B should be deleted")
	}
	if env["A"] != "1" || env["C"] != "3" {
		t.Fatalf("A and C should remain, got: %v", env)
	}
}

func TestSecretSet_MultilineValue(t *testing.T) {
	b := testBackend(t, configServer(t, validCfg(), ""))

	pem := "-----BEGIN CERTIFICATE-----\nMIIBxTCCAWugAwIB\n-----END CERTIFICATE-----"
	if err := b.SecretSet(context.Background(), "TLS_CERT", pem); err != nil {
		t.Fatalf("SecretSet: %v", err)
	}

	env := getEnv(t, b)
	if env["TLS_CERT"] != pem {
		t.Fatalf("multiline value corrupted:\ngot:  %q\nwant: %q", env["TLS_CERT"], pem)
	}
}

// ── IngressSet ────────────────────────────────────────────────────────────────

func TestIngressSet_RegistersDomains(t *testing.T) {
	b := testBackend(t, configServer(t, validCfg(), ""))

	routes := []commands.RouteArg{{Service: "web", Domains: []string{"example.com"}}}
	if err := b.IngressSet(context.Background(), routes, false, "", ""); err != nil {
		t.Fatalf("IngressSet: %v", err)
	}

	cfg := getCfg(t, b)
	domains, ok := cfg.Domains["web"]
	if !ok {
		t.Fatal("web not in domains — route was ignored")
	}
	if len(domains) != 1 || domains[0] != "example.com" {
		t.Fatalf("domains = %v", domains)
	}
}

func TestIngressSet_CloudflareManaged(t *testing.T) {
	initial := validCfg()
	initial.Firewall = &config.FirewallConfig{Preset: "cloudflare"}
	b := testBackend(t, configServer(t, initial, ""))

	routes := []commands.RouteArg{{Service: "web", Domains: []string{"example.com"}}}
	if err := b.IngressSet(context.Background(), routes, true, "", ""); err != nil {
		t.Fatalf("IngressSet: %v", err)
	}

	cfg := getCfg(t, b)
	if cfg.Ingress == nil || !cfg.Ingress.CloudflareManaged {
		t.Fatal("expected cloudflare-managed ingress")
	}
}

func TestIngressSet_CustomCert(t *testing.T) {
	b := testBackend(t, configServer(t, validCfg(), ""))

	routes := []commands.RouteArg{{Service: "web", Domains: []string{"example.com"}}}
	if err := b.IngressSet(context.Background(), routes, false, "---PEM CERT---", "---PEM KEY---"); err != nil {
		t.Fatalf("IngressSet: %v", err)
	}

	// Config stores key names, not raw PEM.
	cfg := getCfg(t, b)
	if cfg.Ingress == nil || cfg.Ingress.Cert != "TLS_CERT_PEM" || cfg.Ingress.Key != "TLS_KEY_PEM" {
		t.Fatalf("ingress = %+v", cfg.Ingress)
	}
	// PEM content stored in env under those key names.
	env := getEnv(t, b)
	if env["TLS_CERT_PEM"] != "---PEM CERT---" || env["TLS_KEY_PEM"] != "---PEM KEY---" {
		t.Fatalf("env = %v", env)
	}
}

// ── ServiceSet ────────────────────────────────────────────────────────────────

func TestServiceSet_PreservesUses(t *testing.T) {
	initial := validCfg()
	initial.Services["db"] = config.Service{Managed: "postgres"}
	initial.Services["web"] = config.Service{
		Workload: config.Workload{Image: "nginx"},
		Port:     80,
		Uses:     []string{"db"},
	}
	b := testBackend(t, configServer(t, initial, ""))

	if err := b.ServiceSet(context.Background(), "web", commands.ServiceOpts{
		WorkloadOpts: commands.WorkloadOpts{Image: "nginx:latest"},
		Port:         80,
	}); err != nil {
		t.Fatalf("ServiceSet: %v", err)
	}

	cfg := getCfg(t, b)
	svc := cfg.Services["web"]
	if svc.Image != "nginx:latest" {
		t.Fatalf("image = %q", svc.Image)
	}
	if len(svc.Uses) == 0 || svc.Uses[0] != "db" {
		t.Fatalf("uses should be preserved, got: %v", svc.Uses)
	}
}

// ── Build ─────────────────────────────────────────────────────────────────────

func TestBuild_InvalidTarget(t *testing.T) {
	b := testBackend(t, configServer(t, validCfg(), ""))

	err := b.Build(context.Background(), commands.BuildOpts{Targets: []string{"nocolon"}})
	if err == nil {
		t.Fatal("expected error for invalid target")
	}
	if !strings.Contains(err.Error(), "expected name:source") {
		t.Fatalf("wrong error: %v", err)
	}
}

func TestBuild_ValidTarget(t *testing.T) {
	b := testBackend(t, configServer(t, validCfg(), ""))

	if err := b.Build(context.Background(), commands.BuildOpts{Targets: []string{"web:./src"}}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	cfg := getCfg(t, b)
	build, ok := cfg.Build["web"]
	if !ok {
		t.Fatal("web not in build")
	}
	if build.Source != "./src" {
		t.Fatalf("source = %q", build.Source)
	}
}

// ── Validate on push ──────────────────────────────────────────────────────────

func TestPushConfig_ValidatesBeforePush(t *testing.T) {
	b := testBackend(t, configServer(t, validCfg(), ""))

	// Volume referencing non-existent server — Validate catches this.
	err := b.VolumeSet(context.Background(), "data", 10, "nonexistent")
	if err == nil {
		t.Fatal("expected validation error for non-existent server")
	}
	if !strings.Contains(err.Error(), "invalid config") {
		t.Fatalf("expected 'invalid config', got: %v", err)
	}
}

// ── DNSSet ────────────────────────────────────────────────────────────────────

func TestDNSSet_AddsDomains(t *testing.T) {
	b := testBackend(t, configServer(t, validCfg(), ""))

	routes := []commands.RouteArg{{Service: "web", Domains: []string{"example.com"}}}
	if err := b.DNSSet(context.Background(), routes, false); err != nil {
		t.Fatalf("DNSSet: %v", err)
	}

	cfg := getCfg(t, b)
	if _, ok := cfg.Domains["web"]; !ok {
		t.Fatal("web not in domains")
	}
}

func TestDNSSet_DoesNotTouchIngress(t *testing.T) {
	initial := validCfg()
	initial.Ingress = &config.IngressConfig{Cert: "TLS_CERT_PEM", Key: "TLS_KEY_PEM"}
	b := testBackend(t, configServer(t, initial, ""))

	routes := []commands.RouteArg{{Service: "web", Domains: []string{"example.com"}}}
	if err := b.DNSSet(context.Background(), routes, true); err != nil {
		t.Fatalf("DNSSet: %v", err)
	}

	// DNS set should NOT overwrite existing ingress config.
	cfg := getCfg(t, b)
	if cfg.Ingress == nil || cfg.Ingress.Cert != "TLS_CERT_PEM" {
		t.Fatalf("DNSSet overwrote ingress config: %+v", cfg.Ingress)
	}
}

func TestDNSSet_CloudflareManagedSetsIngressFlag(t *testing.T) {
	initial := validCfg()
	initial.Firewall = &config.FirewallConfig{Preset: "cloudflare"}
	b := testBackend(t, configServer(t, initial, ""))

	routes := []commands.RouteArg{{Service: "web", Domains: []string{"example.com"}}}
	if err := b.DNSSet(context.Background(), routes, true); err != nil {
		t.Fatalf("DNSSet: %v", err)
	}

	cfg := getCfg(t, b)
	if cfg.Ingress == nil || !cfg.Ingress.CloudflareManaged {
		t.Fatal("dns set --cloudflare-managed should set ingress.cloudflare-managed")
	}
}

// ── DatabaseSet ───────────────────────────────────────────────────────────────

func TestDatabaseSet_ThreadsBackupFields(t *testing.T) {
	b := testBackend(t, configServer(t, validCfg(), ""))

	if err := b.DatabaseSet(context.Background(), "db", commands.ManagedOpts{
		Kind:          "postgres",
		Secrets:       []string{"PG_PASS"},
		BackupStorage: "db-backups",
		BackupCron:    "0 2 * * *",
	}); err != nil {
		t.Fatalf("DatabaseSet: %v", err)
	}

	cfg := getCfg(t, b)
	svc := cfg.Services["db"]
	if svc.Managed != "postgres" {
		t.Fatalf("managed = %q", svc.Managed)
	}
	if svc.BackupStorage != "db-backups" {
		t.Fatalf("backup_storage = %q", svc.BackupStorage)
	}
	if svc.BackupCron != "0 2 * * *" {
		t.Fatalf("backup_cron = %q", svc.BackupCron)
	}
}

func TestDatabaseSet_CustomImage(t *testing.T) {
	b := testBackend(t, configServer(t, validCfg(), ""))

	if err := b.DatabaseSet(context.Background(), "db", commands.ManagedOpts{
		Kind:  "postgres",
		Image: "postgres:16",
	}); err != nil {
		t.Fatalf("DatabaseSet: %v", err)
	}

	cfg := getCfg(t, b)
	if cfg.Services["db"].Image != "postgres:16" {
		t.Fatalf("image = %q", cfg.Services["db"].Image)
	}
}

// ── CronSet / CronDelete ─────────────────────────────────────────────────────

func TestCronSet_AddsCron(t *testing.T) {
	b := testBackend(t, configServer(t, validCfg(), ""))

	if err := b.CronSet(context.Background(), "cleanup", commands.CronOpts{
		WorkloadOpts: commands.WorkloadOpts{Image: "busybox", Command: "echo hi"},
		Schedule:     "0 1 * * *",
	}); err != nil {
		t.Fatalf("CronSet: %v", err)
	}

	cfg := getCfg(t, b)
	cron, ok := cfg.Crons["cleanup"]
	if !ok {
		t.Fatal("cleanup not in crons")
	}
	if cron.Image != "busybox" || cron.Schedule != "0 1 * * *" || cron.Command != "echo hi" {
		t.Fatalf("cron = %+v", cron)
	}
}

func TestCronSet_WithVolumes(t *testing.T) {
	initial := validCfg()
	initial.Volumes = map[string]config.Volume{"data": {Size: 10, Server: "master"}}
	b := testBackend(t, configServer(t, initial, ""))

	if err := b.CronSet(context.Background(), "backup", commands.CronOpts{
		WorkloadOpts: commands.WorkloadOpts{Image: "postgres:17", Volumes: []string{"data:/var/data"}},
		Schedule:     "0 2 * * *",
	}); err != nil {
		t.Fatalf("CronSet: %v", err)
	}

	cfg := getCfg(t, b)
	cron := cfg.Crons["backup"]
	if len(cron.Volumes) != 1 || cron.Volumes[0] != "data:/var/data" {
		t.Fatalf("cron.Volumes = %v, want [data:/var/data]", cron.Volumes)
	}
}

func TestCronDelete_RemovesCron(t *testing.T) {
	initial := validCfg()
	initial.Crons = map[string]config.Cron{"cleanup": {Workload: config.Workload{Image: "busybox"}, Schedule: "0 1 * * *"}}
	b := testBackend(t, configServer(t, initial, ""))

	if err := b.CronDelete(context.Background(), "cleanup"); err != nil {
		t.Fatalf("CronDelete: %v", err)
	}

	cfg := getCfg(t, b)
	if _, ok := cfg.Crons["cleanup"]; ok {
		t.Fatal("cleanup should be deleted")
	}
}

// ── IngressDelete ─────────────────────────────────────────────────────────────

func TestIngressDelete_RemovesTargetedRoute(t *testing.T) {
	initial := validCfg()
	initial.Firewall = &config.FirewallConfig{Preset: "default"}
	initial.Services["api"] = config.Service{Workload: config.Workload{Image: "api:latest"}, Port: 8080}
	initial.Domains = map[string]config.Domains{
		"web": {"example.com"},
		"api": {"api.example.com"},
	}
	b := testBackend(t, configServer(t, initial, ""))

	// Delete only web, keep api.
	routes := []commands.RouteArg{{Service: "web", Domains: []string{"example.com"}}}
	if err := b.IngressDelete(context.Background(), routes, false); err != nil {
		t.Fatalf("IngressDelete: %v", err)
	}

	cfg := getCfg(t, b)
	if _, ok := cfg.Domains["web"]; ok {
		t.Fatal("web should be deleted from domains")
	}
	if _, ok := cfg.Domains["api"]; !ok {
		t.Fatal("api should still exist in domains")
	}
}

func TestIngressDelete_NilsIngressWhenNoDomains(t *testing.T) {
	initial := validCfg()
	initial.Firewall = &config.FirewallConfig{Preset: "cloudflare"}
	initial.Domains = map[string]config.Domains{"web": {"example.com"}}
	initial.Ingress = &config.IngressConfig{CloudflareManaged: true}
	b := testBackend(t, configServer(t, initial, ""))

	routes := []commands.RouteArg{{Service: "web", Domains: []string{"example.com"}}}
	if err := b.IngressDelete(context.Background(), routes, true); err != nil {
		t.Fatalf("IngressDelete: %v", err)
	}

	cfg := getCfg(t, b)
	if len(cfg.Domains) != 0 {
		t.Fatalf("expected no domains, got %v", cfg.Domains)
	}
	if cfg.Ingress != nil {
		t.Fatal("ingress should be nil when no domains remain")
	}
}

func TestIngressDelete_MultiRoute(t *testing.T) {
	initial := validCfg()
	initial.Firewall = &config.FirewallConfig{Preset: "default"}
	initial.Services["api"] = config.Service{Workload: config.Workload{Image: "api:latest"}, Port: 8080}
	initial.Services["admin"] = config.Service{Workload: config.Workload{Image: "admin:latest"}, Port: 9090}
	initial.Domains = map[string]config.Domains{
		"web":   {"example.com"},
		"api":   {"api.example.com"},
		"admin": {"admin.example.com"},
	}
	b := testBackend(t, configServer(t, initial, ""))

	// Delete web and api, keep admin.
	routes := []commands.RouteArg{
		{Service: "web", Domains: []string{"example.com"}},
		{Service: "api", Domains: []string{"api.example.com"}},
	}
	if err := b.IngressDelete(context.Background(), routes, false); err != nil {
		t.Fatalf("IngressDelete: %v", err)
	}

	cfg := getCfg(t, b)
	if _, ok := cfg.Domains["web"]; ok {
		t.Fatal("web should be deleted")
	}
	if _, ok := cfg.Domains["api"]; ok {
		t.Fatal("api should be deleted")
	}
	if _, ok := cfg.Domains["admin"]; !ok {
		t.Fatal("admin should remain")
	}
}
