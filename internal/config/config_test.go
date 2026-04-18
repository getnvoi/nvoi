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
  infra: hetzner
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
	if cfg.Providers.Infra != "hetzner" {
		t.Errorf("Providers.Infra = %q", cfg.Providers.Infra)
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

// ── BuildSpec YAML shapes ──────────────────────────────────────────────────

func TestBuildSpec_BoolTrue(t *testing.T) {
	yaml := []byte(`
app: myapp
env: prod
providers:
  compute: hetzner
servers:
  master:
    type: cax11
    region: nbg1
    role: master
services:
  api:
    image: ghcr.io/org/api:v1
    build: true
`)
	cfg, err := ParseAppConfig(yaml)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	svc := cfg.Services["api"]
	if svc.Build == nil {
		t.Fatal("build: true should produce non-nil BuildSpec")
	}
	if svc.Build.Context != "./" {
		t.Errorf("build: true context = %q, want %q", svc.Build.Context, "./")
	}
}

func TestBuildSpec_BoolFalse(t *testing.T) {
	yaml := []byte(`
app: myapp
env: prod
providers:
  compute: hetzner
servers:
  master:
    type: cax11
    region: nbg1
    role: master
services:
  api:
    image: ghcr.io/org/api:v1
    build: false
`)
	cfg, err := ParseAppConfig(yaml)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	svc := cfg.Services["api"]
	// build: false → non-nil pointer but empty Context, downstream treats as "no build"
	if svc.Build != nil && svc.Build.Context != "" {
		t.Errorf("build: false should have empty Context, got %q", svc.Build.Context)
	}
}

func TestBuildSpec_StringPath(t *testing.T) {
	yaml := []byte(`
app: myapp
env: prod
providers:
  compute: hetzner
servers:
  master:
    type: cax11
    region: nbg1
    role: master
services:
  api:
    image: ghcr.io/org/api:v1
    build: services/api
`)
	cfg, err := ParseAppConfig(yaml)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	svc := cfg.Services["api"]
	if svc.Build == nil || svc.Build.Context != "services/api" {
		t.Errorf("build: services/api = %+v, want Context services/api", svc.Build)
	}
}

func TestBuildSpec_MapFormWithContextAndDockerfile(t *testing.T) {
	// Monorepo pattern: Dockerfile lives in a subdir but the build
	// context needs the whole repo (e.g. Go builds that COPY go.mod
	// from the root).
	yaml := []byte(`
app: myapp
env: prod
providers:
  compute: hetzner
servers:
  master:
    type: cax11
    region: nbg1
    role: master
services:
  api:
    image: ghcr.io/org/api:v1
    build:
      context: ./
      dockerfile: ./cmd/api/Dockerfile
`)
	cfg, err := ParseAppConfig(yaml)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	b := cfg.Services["api"].Build
	if b == nil {
		t.Fatal("Build should be non-nil")
	}
	if b.Context != "./" {
		t.Errorf("context = %q, want ./", b.Context)
	}
	if b.Dockerfile != "./cmd/api/Dockerfile" {
		t.Errorf("dockerfile = %q, want ./cmd/api/Dockerfile", b.Dockerfile)
	}
}

// INVARIANT: omitting `context:` from the struct form defaults it to
// "./" so the common monorepo case stays concise.
//
//	build:
//	  dockerfile: ./cmd/api/Dockerfile   # context implicitly "./"
func TestBuildSpec_MapFormDefaultsContextToDot(t *testing.T) {
	yaml := []byte(`
app: myapp
env: prod
providers:
  compute: hetzner
servers:
  master:
    type: cax11
    region: nbg1
    role: master
services:
  api:
    image: ghcr.io/org/api:v1
    build:
      dockerfile: ./cmd/api/Dockerfile
`)
	cfg, err := ParseAppConfig(yaml)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	b := cfg.Services["api"].Build
	if b == nil {
		t.Fatal("Build should be non-nil")
	}
	if b.Context != "./" {
		t.Errorf("implicit context = %q, want ./", b.Context)
	}
	if b.Dockerfile != "./cmd/api/Dockerfile" {
		t.Errorf("dockerfile = %q", b.Dockerfile)
	}
}

func TestBuildSpec_Absent(t *testing.T) {
	yaml := []byte(`
app: myapp
env: prod
providers:
  compute: hetzner
servers:
  master:
    type: cax11
    region: nbg1
    role: master
services:
  api:
    image: docker.io/library/nginx
`)
	cfg, err := ParseAppConfig(yaml)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Services["api"].Build != nil {
		t.Error("absent build: should leave Build == nil")
	}
}

// ── providers.secrets YAML shapes ────────────────────────────────────────────

func TestProvidersSecrets_ScalarShape(t *testing.T) {
	yaml := []byte(`
app: myapp
env: prod
providers:
  compute: hetzner
  secrets: doppler
servers:
  master:
    type: cax11
    region: nbg1
    role: master
`)
	cfg, err := ParseAppConfig(yaml)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Providers.Secrets == nil {
		t.Fatal("Secrets pointer should be non-nil")
	}
	if cfg.Providers.Secrets.Kind != "doppler" {
		t.Errorf("Kind = %q, want doppler", cfg.Providers.Secrets.Kind)
	}
}

func TestProvidersSecrets_StructShape(t *testing.T) {
	yaml := []byte(`
app: myapp
env: prod
providers:
  compute: hetzner
  secrets:
    kind: infisical
servers:
  master:
    type: cax11
    region: nbg1
    role: master
`)
	cfg, err := ParseAppConfig(yaml)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Providers.Secrets == nil || cfg.Providers.Secrets.Kind != "infisical" {
		t.Errorf("Secrets = %+v, want {Kind: infisical}", cfg.Providers.Secrets)
	}
}

func TestProvidersSecrets_Absent(t *testing.T) {
	yaml := []byte(`
app: myapp
env: prod
providers:
  compute: hetzner
servers:
  master:
    type: cax11
    region: nbg1
    role: master
`)
	cfg, err := ParseAppConfig(yaml)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Providers.Secrets != nil {
		t.Errorf("Secrets should be nil when absent, got %+v", cfg.Providers.Secrets)
	}
}
