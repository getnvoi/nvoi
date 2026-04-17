package config

import (
	"testing"

	"github.com/getnvoi/nvoi/pkg/utils"
)

func TestResolve_VolumeMountPaths(t *testing.T) {
	cfg := &AppConfig{
		App: "myapp",
		Env: "production",
		Volumes: map[string]VolumeDef{
			"pgdata":  {Size: 20, Server: "master"},
			"uploads": {Size: 50, Server: "worker"},
		},
	}
	if err := cfg.Resolve(); err != nil {
		t.Fatal(err)
	}

	names, _ := utils.NewNames("myapp", "production")

	// Each volume's MountPath must equal names.VolumeMountPath with the volume config key.
	for volName, vol := range cfg.Volumes {
		want := names.VolumeMountPath(volName)
		if vol.MountPath != want {
			t.Errorf("Volumes[%q].MountPath = %q, want %q", volName, vol.MountPath, want)
		}
	}

	// Spot-check the actual path format
	if got := cfg.Volumes["pgdata"].MountPath; got != "/mnt/data/nvoi-myapp-production-pgdata" {
		t.Errorf("pgdata.MountPath = %q, want /mnt/data/nvoi-myapp-production-pgdata", got)
	}
}

func TestResolve_FirewallNames(t *testing.T) {
	cfg := &AppConfig{App: "myapp", Env: "production"}
	if err := cfg.Resolve(); err != nil {
		t.Fatal(err)
	}
	if cfg.MasterFirewall != "nvoi-myapp-production-master-fw" {
		t.Errorf("MasterFirewall = %q", cfg.MasterFirewall)
	}
	if cfg.WorkerFirewall != "nvoi-myapp-production-worker-fw" {
		t.Errorf("WorkerFirewall = %q", cfg.WorkerFirewall)
	}
}

func TestResolve_EmptyConfig(t *testing.T) {
	// Resolve on config with no volumes should succeed.
	cfg := &AppConfig{
		App: "myapp",
		Env: "production",
	}
	if err := cfg.Resolve(); err != nil {
		t.Fatalf("Resolve failed on empty config: %v", err)
	}
}

func TestResolve_MissingAppEnv(t *testing.T) {
	cfg := &AppConfig{}
	if err := cfg.Resolve(); err == nil {
		t.Fatal("expected error when app/env are empty")
	}
}

func TestParseAppConfig_CoreShape(t *testing.T) {
	yaml := `
app: myapp
env: prod
providers:
  compute: hetzner
servers:
  master:
    type: cx23
    region: fsn1
    role: master
services:
  web:
    image: nginx
`
	cfg, err := ParseAppConfig([]byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.App != "myapp" || cfg.Env != "prod" {
		t.Errorf("app/env = %q/%q", cfg.App, cfg.Env)
	}
	if cfg.Providers.Compute != "hetzner" {
		t.Errorf("Providers.Compute = %q", cfg.Providers.Compute)
	}
}

func TestStorageNames(t *testing.T) {
	cfg := &AppConfig{
		Storage: map[string]StorageDef{
			"assets":  {},
			"uploads": {},
		},
	}
	names := cfg.StorageNames()
	if len(names) != 2 {
		t.Fatalf("got %d names, want 2", len(names))
	}
}

func TestStorageNames_Empty(t *testing.T) {
	cfg := &AppConfig{}
	names := cfg.StorageNames()
	if len(names) != 0 {
		t.Fatalf("got %d names, want 0", len(names))
	}
}
