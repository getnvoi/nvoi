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

func TestValidateConfig_InvalidAppName(t *testing.T) {
	cfg := validCfg()
	cfg.App = "My App!"
	assertValidationError(t, cfg, "is invalid")
}

func TestValidateConfig_InvalidEnvName(t *testing.T) {
	cfg := validCfg()
	cfg.Env = "PRODUCTION"
	assertValidationError(t, cfg, "is invalid")
}

func TestValidateConfig_InvalidServerName(t *testing.T) {
	cfg := validCfg()
	cfg.Servers = map[string]config.ServerDef{
		"MY_SERVER": {Type: "cx23", Region: "fsn1", Role: "master"},
	}
	assertValidationError(t, cfg, "is invalid")
}

func TestValidateConfig_InvalidServiceName(t *testing.T) {
	cfg := validCfg()
	cfg.Services = map[string]config.ServiceDef{
		"my_service": {Image: "nginx", Port: 80},
	}
	assertValidationError(t, cfg, "is invalid")
}

func TestValidateConfig_InvalidCronName(t *testing.T) {
	cfg := validCfg()
	cfg.Crons = map[string]config.CronDef{
		"My Cron": {Image: "busybox", Schedule: "0 * * * *", Command: "echo"},
	}
	assertValidationError(t, cfg, "is invalid")
}

func TestValidateConfig_InvalidVolumeName(t *testing.T) {
	cfg := validCfg()
	cfg.Volumes = map[string]config.VolumeDef{
		"pg.data": {Size: 20, Server: "master"},
	}
	assertValidationError(t, cfg, "is invalid")
}

func TestValidateConfig_InvalidSecretName(t *testing.T) {
	cfg := validCfg()
	cfg.Secrets = []string{"DATABASE/URL"}
	assertValidationError(t, cfg, "is invalid")
}

func TestValidateConfig_InvalidSecretNameLowercase(t *testing.T) {
	cfg := validCfg()
	cfg.Secrets = []string{"jwt_secret"}
	assertValidationError(t, cfg, "is invalid")
}

func TestValidateConfig_ValidSecretNames(t *testing.T) {
	cfg := validCfg()
	cfg.Secrets = []string{"JWT_SECRET", "ENCRYPTION_KEY", "DB_PASSWORD"}
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("valid secret names should pass: %v", err)
	}
}

func TestValidateConfig_ValidHyphenatedNames(t *testing.T) {
	cfg := validCfg()
	cfg.App = "my-app"
	cfg.Env = "staging-eu"
	cfg.Services = map[string]config.ServiceDef{
		"web-frontend": {Image: "nginx", Port: 80},
	}
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("hyphenated names should be valid: %v", err)
	}
}

func TestValidateConfig_NegativeDisk(t *testing.T) {
	cfg := validCfg()
	cfg.Servers["master"] = config.ServerDef{Type: "cx23", Region: "fsn1", Role: "master", Disk: -1}
	assertValidationError(t, cfg, "disk must be >= 0")
}

func TestValidateConfig_DiskWithHetzner(t *testing.T) {
	cfg := validCfg()
	cfg.Providers.Compute = "hetzner"
	cfg.Servers["master"] = config.ServerDef{Type: "cx23", Region: "fsn1", Role: "master", Disk: 100}
	assertValidationError(t, cfg, "hetzner does not support custom root disk sizes")
}

func TestValidateConfig_DiskWithScaleway(t *testing.T) {
	cfg := validCfg()
	cfg.Providers.Compute = "scaleway"
	cfg.Servers["master"] = config.ServerDef{Type: "DEV1-M", Region: "fr-par-1", Role: "master", Disk: 50}
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("disk with scaleway should be valid: %v", err)
	}
}

func TestValidateConfig_DiskWithAWS(t *testing.T) {
	cfg := validCfg()
	cfg.Providers.Compute = "aws"
	cfg.Servers["master"] = config.ServerDef{Type: "t3.medium", Region: "us-east-1", Role: "master", Disk: 100}
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("disk with aws should be valid: %v", err)
	}
}

func TestValidateConfig_DiskOmitted(t *testing.T) {
	cfg := validCfg()
	cfg.Providers.Compute = "hetzner"
	// Disk is 0 (omitted) — should pass even for Hetzner
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("omitted disk should be valid: %v", err)
	}
}

func TestValidateConfig_ServiceSecretValidBareName(t *testing.T) {
	cfg := validCfg()
	cfg.Services["web"] = config.ServiceDef{Image: "nginx", Port: 80, Secrets: []string{"JWT_SECRET"}}
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("valid bare secret name should pass: %v", err)
	}
}

func TestValidateConfig_ServiceSecretValidAliasWithDollar(t *testing.T) {
	cfg := validCfg()
	cfg.Services["web"] = config.ServiceDef{Image: "nginx", Port: 80, Secrets: []string{"SECRET_KEY=$BUGSINK_SECRET_KEY"}}
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("valid aliased secret ref with $ should pass: %v", err)
	}
}

func TestValidateConfig_ServiceSecretEqualsWithoutDollar(t *testing.T) {
	cfg := validCfg()
	cfg.Services["web"] = config.ServiceDef{Image: "nginx", Port: 80, Secrets: []string{"SECRET_KEY=BUGSINK_SECRET_KEY"}}
	assertValidationError(t, cfg, "requires $")
}

func TestValidateConfig_ServiceSecretInvalidEnvName(t *testing.T) {
	cfg := validCfg()
	cfg.Services["web"] = config.ServiceDef{Image: "nginx", Port: 80, Secrets: []string{"secret-key=$FOO"}}
	assertValidationError(t, cfg, "is invalid")
}

func TestValidateConfig_ServiceSecretDollarSkipsKeyValidation(t *testing.T) {
	cfg := validCfg()
	cfg.Services["web"] = config.ServiceDef{Image: "nginx", Port: 80, Secrets: []string{"DB_URL=$MAIN_DATABASE_URL"}}
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("$VAR in secret key should skip key validation: %v", err)
	}
}

func TestValidateConfig_ServiceSecretComposedDollar(t *testing.T) {
	cfg := validCfg()
	cfg.Services["web"] = config.ServiceDef{Image: "nginx", Port: 80, Secrets: []string{"AUTH=$USER:$PASS"}}
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("composed $VAR should pass: %v", err)
	}
}

func TestValidateConfig_CronSecretInvalidName(t *testing.T) {
	cfg := validCfg()
	cfg.Crons = map[string]config.CronDef{
		"job": {Image: "busybox", Schedule: "0 * * * *", Secrets: []string{"bad-name"}},
	}
	assertValidationError(t, cfg, "is invalid")
}

func TestValidateConfig_CronSecretEqualsWithoutDollar(t *testing.T) {
	cfg := validCfg()
	cfg.Crons = map[string]config.CronDef{
		"job": {Image: "busybox", Schedule: "0 * * * *", Secrets: []string{"FOO=BAR"}},
	}
	assertValidationError(t, cfg, "requires $")
}

// ── Secrets provider validation ─────────────────────────────────────────────

func TestValidateConfig_SecretsProviderValid(t *testing.T) {
	cfg := validCfg()
	cfg.Providers.Secrets = "doppler"
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateConfig_SecretsProviderUnsupportedKind(t *testing.T) {
	cfg := validCfg()
	cfg.Providers.Secrets = "vault"
	assertValidationError(t, cfg, "unsupported secrets provider")
}

func TestValidateConfig_SecretsProviderAllKinds(t *testing.T) {
	for _, kind := range []string{"doppler", "awssm", "infisical"} {
		cfg := validCfg()
		cfg.Providers.Secrets = kind
		if err := ValidateConfig(cfg); err != nil {
			t.Fatalf("kind %q: unexpected error: %v", kind, err)
		}
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
