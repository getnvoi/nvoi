package reconcile

import (
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
)

func TestValidateConfig_Valid(t *testing.T) {
	if err := ValidateConfig(validCfg()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateConfig_MissingApp(t *testing.T) {
	cfg := validCfg()
	cfg.App = ""
	assertValidationError(t, cfg, "app is required")
}

func TestValidateConfig_MissingCompute(t *testing.T) {
	cfg := validCfg()
	cfg.Providers.Compute = ""
	assertValidationError(t, cfg, "providers.compute is required")
}

func TestValidateConfig_NoServers(t *testing.T) {
	cfg := validCfg()
	cfg.Servers = nil
	assertValidationError(t, cfg, "at least one server")
}

func TestValidateConfig_NoMaster(t *testing.T) {
	cfg := validCfg()
	cfg.Servers = map[string]config.ServerDef{"w": {Type: "cx23", Region: "fsn1", Role: "worker"}}
	assertValidationError(t, cfg, "role: master")
}

func TestValidateConfig_ServerMissingType(t *testing.T) {
	cfg := validCfg()
	cfg.Servers["master"] = config.ServerDef{Region: "fsn1", Role: "master"}
	assertValidationError(t, cfg, "type is required")
}

func TestValidateConfig_VolumeServerNotDefined(t *testing.T) {
	cfg := validCfg()
	cfg.Volumes = map[string]config.VolumeDef{"pgdata": {Size: 20, Server: "nonexistent"}}
	assertValidationError(t, cfg, "not a defined server")
}

func TestValidateConfig_ServiceNoImage(t *testing.T) {
	cfg := validCfg()
	cfg.Services["web"] = config.ServiceDef{}
	assertValidationError(t, cfg, "image or build is required")
}

func TestValidateConfig_ServiceBuildNotDefined(t *testing.T) {
	cfg := validCfg()
	cfg.Services["web"] = config.ServiceDef{Build: "missing"}
	assertValidationError(t, cfg, "not a defined build target")
}

func TestValidateConfig_ServiceStorageNotDefined(t *testing.T) {
	cfg := validCfg()
	cfg.Services["web"] = config.ServiceDef{Image: "nginx", Storage: []string{"missing"}}
	assertValidationError(t, cfg, "not a defined storage")
}

func TestValidateConfig_ServiceVolumeNotDefined(t *testing.T) {
	cfg := validCfg()
	cfg.Services["web"] = config.ServiceDef{Image: "nginx", Volumes: []string{"missing:/data"}}
	assertValidationError(t, cfg, "volume \"missing\" is not defined")
}

func TestValidateConfig_ServiceVolumeBadFormat(t *testing.T) {
	cfg := validCfg()
	cfg.Services["web"] = config.ServiceDef{Image: "nginx", Volumes: []string{"nocolon"}}
	assertValidationError(t, cfg, "must be name:/path")
}

func TestValidateConfig_DomainServiceMissing(t *testing.T) {
	cfg := validCfg()
	cfg.Domains = map[string][]string{"api": {"api.example.com"}}
	assertValidationError(t, cfg, "not a defined service")
}

func TestValidateConfig_DomainServiceNoPort(t *testing.T) {
	cfg := validCfg()
	cfg.Services["web"] = config.ServiceDef{Image: "nginx"}
	cfg.Domains = map[string][]string{"web": {"example.com"}}
	assertValidationError(t, cfg, "has no port")
}

func TestValidateConfig_VolumeServerMismatch(t *testing.T) {
	cfg := validCfg()
	cfg.Servers["worker-1"] = config.ServerDef{Type: "cx33", Region: "fsn1", Role: "worker"}
	cfg.Volumes = map[string]config.VolumeDef{"pgdata": {Size: 20, Server: "master"}}
	cfg.Services["db"] = config.ServiceDef{Image: "postgres:17", Server: "worker-1", Volumes: []string{"pgdata:/data"}}
	assertValidationError(t, cfg, "cannot move")
}

func TestValidateConfig_VolumeServerMatch(t *testing.T) {
	cfg := validCfg()
	cfg.Volumes = map[string]config.VolumeDef{"pgdata": {Size: 20, Server: "master"}}
	cfg.Services["db"] = config.ServiceDef{Image: "postgres:17", Server: "master", Volumes: []string{"pgdata:/data"}}
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidateConfig_NoServerKeyAllowed(t *testing.T) {
	cfg := validCfg()
	cfg.Volumes = map[string]config.VolumeDef{"pgdata": {Size: 20, Server: "master"}}
	cfg.Services["db"] = config.ServiceDef{Image: "postgres:17", Volumes: []string{"pgdata:/data"}}
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("expected no error (no server key = ok), got: %v", err)
	}
}

func TestValidateConfig_ServerAndServersMutuallyExclusive(t *testing.T) {
	cfg := validCfg()
	cfg.Servers["worker-1"] = config.ServerDef{Type: "cx33", Region: "fsn1", Role: "worker"}
	cfg.Services["web"] = config.ServiceDef{Image: "nginx", Server: "master", Servers: []string{"master", "worker-1"}}
	assertValidationError(t, cfg, "server and servers are mutually exclusive")
}

func TestValidateConfig_ServersReferencesValid(t *testing.T) {
	cfg := validCfg()
	cfg.Servers["worker-1"] = config.ServerDef{Type: "cx33", Region: "fsn1", Role: "worker"}
	cfg.Services["web"] = config.ServiceDef{Image: "nginx", Servers: []string{"master", "worker-1"}}
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidateConfig_ServersReferencesInvalid(t *testing.T) {
	cfg := validCfg()
	cfg.Services["web"] = config.ServiceDef{Image: "nginx", Servers: []string{"master", "nonexistent"}}
	assertValidationError(t, cfg, "not a defined server")
}

func TestValidateConfig_CronServerAndServersMutuallyExclusive(t *testing.T) {
	cfg := validCfg()
	cfg.Servers["worker-1"] = config.ServerDef{Type: "cx33", Region: "fsn1", Role: "worker"}
	cfg.Crons = map[string]config.CronDef{
		"job": {Image: "busybox", Schedule: "0 * * * *", Server: "master", Servers: []string{"master", "worker-1"}},
	}
	assertValidationError(t, cfg, "server and servers are mutually exclusive")
}

func TestValidateConfig_MultipleServersWithVolume(t *testing.T) {
	cfg := validCfg()
	cfg.Servers["worker-1"] = config.ServerDef{Type: "cx33", Region: "fsn1", Role: "worker"}
	cfg.Volumes = map[string]config.VolumeDef{"pgdata": {Size: 20, Server: "master"}}
	cfg.Services["db"] = config.ServiceDef{
		Image:   "postgres:17",
		Servers: []string{"master", "worker-1"},
		Volumes: []string{"pgdata:/data"},
	}
	assertValidationError(t, cfg, "multiple servers with a volume mount")
}

func TestValidateConfig_SingleServerWithVolumeOK(t *testing.T) {
	cfg := validCfg()
	cfg.Volumes = map[string]config.VolumeDef{"pgdata": {Size: 20, Server: "master"}}
	cfg.Services["db"] = config.ServiceDef{
		Image:   "postgres:17",
		Servers: []string{"master"},
		Volumes: []string{"pgdata:/data"},
	}
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("single server with volume should be ok, got: %v", err)
	}
}

func TestValidateConfig_WebFacingExplicit1Replica_Error(t *testing.T) {
	cfg := validCfg()
	cfg.Services["web"] = config.ServiceDef{Image: "nginx", Port: 80, Replicas: 1}
	cfg.Domains = map[string][]string{"web": {"example.com"}}
	assertValidationError(t, cfg, "replicas >= 2")
}

func TestValidateConfig_WebFacingOmittedReplicas_OK(t *testing.T) {
	cfg := validCfg()
	cfg.Services["web"] = config.ServiceDef{Image: "nginx", Port: 80} // Replicas: 0 (omitted)
	cfg.Domains = map[string][]string{"web": {"example.com"}}
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("omitted replicas on web-facing service should be ok (defaults to 2), got: %v", err)
	}
}

func TestValidateConfig_WebFacingExplicit2Replicas_OK(t *testing.T) {
	cfg := validCfg()
	cfg.Services["web"] = config.ServiceDef{Image: "nginx", Port: 80, Replicas: 2}
	cfg.Domains = map[string][]string{"web": {"example.com"}}
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("explicit 2 replicas should be ok, got: %v", err)
	}
}

func TestValidateConfig_NoDomainSingleReplica_OK(t *testing.T) {
	cfg := validCfg()
	cfg.Services["worker"] = config.ServiceDef{Image: "busybox", Replicas: 1}
	// No domain — single replica is fine
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("no domain + 1 replica should be ok, got: %v", err)
	}
}

func TestResolveServers_ExplicitServers(t *testing.T) {
	cfg := &config.AppConfig{}
	got := ResolveServers(cfg, []string{"worker-1", "worker-2"}, "", nil)
	if len(got) != 2 || got[0] != "worker-1" || got[1] != "worker-2" {
		t.Errorf("expected [worker-1, worker-2], got %v", got)
	}
}

func TestResolveServers_SingleServer(t *testing.T) {
	cfg := &config.AppConfig{}
	got := ResolveServers(cfg, nil, "master", nil)
	if len(got) != 1 || got[0] != "master" {
		t.Errorf("expected [master], got %v", got)
	}
}

func TestResolveServers_AutoPinFromVolume(t *testing.T) {
	cfg := &config.AppConfig{
		Volumes: map[string]config.VolumeDef{"pgdata": {Size: 20, Server: "master"}},
	}
	got := ResolveServers(cfg, nil, "", []string{"pgdata:/data"})
	if len(got) != 1 || got[0] != "master" {
		t.Errorf("expected [master] from volume pin, got %v", got)
	}
}

func TestResolveServers_NoServerNoVolume(t *testing.T) {
	cfg := &config.AppConfig{}
	got := ResolveServers(cfg, nil, "", nil)
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestResolveServers_ExplicitOverridesVolume(t *testing.T) {
	cfg := &config.AppConfig{
		Volumes: map[string]config.VolumeDef{"pgdata": {Size: 20, Server: "master"}},
	}
	// Explicit servers takes precedence over volume auto-pin
	got := ResolveServers(cfg, []string{"worker-1"}, "", []string{"pgdata:/data"})
	if len(got) != 1 || got[0] != "worker-1" {
		t.Errorf("expected [worker-1], got %v", got)
	}
}

func assertValidationError(t *testing.T, cfg *config.AppConfig, substr string) {
	t.Helper()
	err := ValidateConfig(cfg)
	if err == nil {
		t.Fatalf("expected error containing %q, got nil", substr)
	}
	if !strings.Contains(err.Error(), substr) {
		t.Fatalf("expected error containing %q, got: %s", substr, err)
	}
}
