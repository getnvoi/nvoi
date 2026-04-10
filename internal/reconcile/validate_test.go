package reconcile

import (
	"strings"
	"testing"
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
	cfg.Servers = map[string]ServerDef{"w": {Type: "cx23", Region: "fsn1", Role: "worker"}}
	assertValidationError(t, cfg, "role: master")
}

func TestValidateConfig_ServerMissingType(t *testing.T) {
	cfg := validCfg()
	cfg.Servers["master"] = ServerDef{Region: "fsn1", Role: "master"}
	assertValidationError(t, cfg, "type is required")
}

func TestValidateConfig_VolumeServerNotDefined(t *testing.T) {
	cfg := validCfg()
	cfg.Volumes = map[string]VolumeDef{"pgdata": {Size: 20, Server: "nonexistent"}}
	assertValidationError(t, cfg, "not a defined server")
}

func TestValidateConfig_ServiceNoImage(t *testing.T) {
	cfg := validCfg()
	cfg.Services["web"] = ServiceDef{}
	assertValidationError(t, cfg, "image or build is required")
}

func TestValidateConfig_ServiceBuildNotDefined(t *testing.T) {
	cfg := validCfg()
	cfg.Services["web"] = ServiceDef{Build: "missing"}
	assertValidationError(t, cfg, "not a defined build target")
}

func TestValidateConfig_ServiceStorageNotDefined(t *testing.T) {
	cfg := validCfg()
	cfg.Services["web"] = ServiceDef{Image: "nginx", Storage: []string{"missing"}}
	assertValidationError(t, cfg, "not a defined storage")
}

func TestValidateConfig_ServiceVolumeNotDefined(t *testing.T) {
	cfg := validCfg()
	cfg.Services["web"] = ServiceDef{Image: "nginx", Volumes: []string{"missing:/data"}}
	assertValidationError(t, cfg, "volume \"missing\" is not defined")
}

func TestValidateConfig_ServiceVolumeBadFormat(t *testing.T) {
	cfg := validCfg()
	cfg.Services["web"] = ServiceDef{Image: "nginx", Volumes: []string{"nocolon"}}
	assertValidationError(t, cfg, "must be name:/path")
}

func TestValidateConfig_DomainServiceMissing(t *testing.T) {
	cfg := validCfg()
	cfg.Domains = map[string][]string{"api": {"api.example.com"}}
	assertValidationError(t, cfg, "not a defined service")
}

func TestValidateConfig_DomainServiceNoPort(t *testing.T) {
	cfg := validCfg()
	cfg.Services["web"] = ServiceDef{Image: "nginx"}
	cfg.Domains = map[string][]string{"web": {"example.com"}}
	assertValidationError(t, cfg, "has no port")
}

func TestValidateConfig_VolumeServerMismatch(t *testing.T) {
	cfg := validCfg()
	cfg.Servers["worker-1"] = ServerDef{Type: "cx33", Region: "fsn1", Role: "worker"}
	cfg.Volumes = map[string]VolumeDef{"pgdata": {Size: 20, Server: "master"}}
	cfg.Services["db"] = ServiceDef{Image: "postgres:17", Server: "worker-1", Volumes: []string{"pgdata:/data"}}
	assertValidationError(t, cfg, "cannot move")
}

func TestValidateConfig_VolumeServerMatch(t *testing.T) {
	cfg := validCfg()
	cfg.Volumes = map[string]VolumeDef{"pgdata": {Size: 20, Server: "master"}}
	cfg.Services["db"] = ServiceDef{Image: "postgres:17", Server: "master", Volumes: []string{"pgdata:/data"}}
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidateConfig_NoServerKeyAllowed(t *testing.T) {
	cfg := validCfg()
	cfg.Volumes = map[string]VolumeDef{"pgdata": {Size: 20, Server: "master"}}
	cfg.Services["db"] = ServiceDef{Image: "postgres:17", Volumes: []string{"pgdata:/data"}}
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("expected no error (no server key = ok), got: %v", err)
	}
}

func assertValidationError(t *testing.T, cfg *AppConfig, substr string) {
	t.Helper()
	err := ValidateConfig(cfg)
	if err == nil {
		t.Fatalf("expected error containing %q, got nil", substr)
	}
	if !strings.Contains(err.Error(), substr) {
		t.Fatalf("expected error containing %q, got: %s", substr, err)
	}
}
