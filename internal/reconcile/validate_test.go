package reconcile

import (
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/pkg/provider"
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

func TestValidateConfig_MissingInfra(t *testing.T) {
	cfg := validCfg()
	cfg.Providers.Infra = ""
	assertValidationError(t, cfg, "providers.infra is required")
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
	assertValidationError(t, cfg, "image is required")
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
	cfg.Providers.Infra = "hetzner"
	cfg.Servers["master"] = config.ServerDef{Type: "cx23", Region: "fsn1", Role: "master", Disk: 100}
	assertValidationError(t, cfg, "hetzner does not support custom root disk sizes")
}

func TestValidateConfig_DiskWithScaleway(t *testing.T) {
	cfg := validCfg()
	cfg.Providers.Infra = "scaleway"
	cfg.Servers["master"] = config.ServerDef{Type: "DEV1-M", Region: "fr-par-1", Role: "master", Disk: 50}
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("disk with scaleway should be valid: %v", err)
	}
}

func TestValidateConfig_DiskWithAWS(t *testing.T) {
	cfg := validCfg()
	cfg.Providers.Infra = "aws"
	cfg.Servers["master"] = config.ServerDef{Type: "t3.medium", Region: "us-east-1", Role: "master", Disk: 100}
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("disk with aws should be valid: %v", err)
	}
}

func TestValidateConfig_DiskOmitted(t *testing.T) {
	cfg := validCfg()
	cfg.Providers.Infra = "hetzner"
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

// ── Firewall + tunnel validation ─────────────────────────────────────────────

// validTunnelCfg returns a minimal config in tunnel mode (cloudflare).
// domains and dns are required by the tunnel validator.
func validTunnelCfg() *config.AppConfig {
	cfg := validCfg()
	cfg.Providers.DNS = "cloudflare"
	cfg.Providers.Tunnel = "cloudflare"
	cfg.Services["api"] = config.ServiceDef{Image: "myapp/api", Port: 8080}
	cfg.Domains = map[string][]string{"api": {"api.example.com"}}
	return cfg
}

func TestValidateConfig_TunnelMode_Firewall80_Errors(t *testing.T) {
	cfg := validTunnelCfg()
	cfg.Firewall = []string{"80:0.0.0.0/0"}
	assertValidationError(t, cfg, "port 80 cannot be opened in tunnel mode")
}

func TestValidateConfig_TunnelMode_Firewall443_Errors(t *testing.T) {
	cfg := validTunnelCfg()
	cfg.Firewall = []string{"443:0.0.0.0/0"}
	assertValidationError(t, cfg, "port 443 cannot be opened in tunnel mode")
}

func TestValidateConfig_TunnelMode_NonHTTPFirewallRule_Ok(t *testing.T) {
	cfg := validTunnelCfg()
	cfg.Firewall = []string{"8080:10.0.0.0/8"} // custom port, not 80/443
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("non-HTTP firewall rule in tunnel mode should be valid, got: %v", err)
	}
}

func TestValidateConfig_CaddyMode_Firewall80_Ok(t *testing.T) {
	// 80/443 in firewall is fine in Caddy mode (no tunnel).
	cfg := validCfg()
	cfg.Providers.DNS = "cloudflare"
	cfg.Services["api"] = config.ServiceDef{Image: "myapp/api", Port: 8080}
	cfg.Domains = map[string][]string{"api": {"api.example.com"}}
	cfg.Firewall = []string{"80:1.2.3.4/32"}
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("80 in firewall in Caddy mode should be valid, got: %v", err)
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

// ── Build validation ──────────────────────────────────────────────────────

func TestValidateConfig_BuildWithoutRegistryBlock_Errors(t *testing.T) {
	cfg := validCfgForTest()
	cfg.Services["web"] = config.ServiceDef{
		Image: "ghcr.io/org/web:v1",
		Build: &config.BuildSpec{Context: "./"},
	}
	assertValidationError(t, cfg, "registry:")
}

func TestValidateConfig_BuildBareImageName_Errors(t *testing.T) {
	cfg := validCfgForTest()
	cfg.Registry = map[string]config.RegistryDef{
		"ghcr.io": {Username: "u", Password: "p"},
	}
	cfg.Services["web"] = config.ServiceDef{
		Image: "nginx", // bare shortname — no repo namespace (would push to <host>/nginx)
		Build: &config.BuildSpec{Context: "./"},
	}
	assertValidationError(t, cfg, "no repo namespace")
}

// Kamal-style: repo-only image with exactly ONE registry declared is
// valid — the host is inferred from the single registry entry.
func TestValidateConfig_BuildRepoOnlyWithSingleRegistry_Valid(t *testing.T) {
	cfg := validCfgForTest()
	cfg.Registry = map[string]config.RegistryDef{
		"ghcr.io": {Username: "u", Password: "p"},
	}
	cfg.Services["web"] = config.ServiceDef{
		Image: "org/web",
		Build: &config.BuildSpec{Context: "./"},
	}
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("expected valid, got: %v", err)
	}
}

// Two registries declared + no host prefix on image = ambiguous, must error.
func TestValidateConfig_BuildRepoOnlyWithMultipleRegistries_Ambiguous(t *testing.T) {
	cfg := validCfgForTest()
	cfg.Registry = map[string]config.RegistryDef{
		"ghcr.io":   {Username: "u", Password: "p"},
		"docker.io": {Username: "u", Password: "p"},
	}
	cfg.Services["web"] = config.ServiceDef{
		Image: "org/web", // no host prefix → ambiguous with multiple registries
		Build: &config.BuildSpec{Context: "./"},
	}
	assertValidationError(t, cfg, "multiple registries")
}

func TestValidateConfig_BuildHostNotInRegistry_Errors(t *testing.T) {
	cfg := validCfgForTest()
	cfg.Registry = map[string]config.RegistryDef{
		"docker.io": {Username: "u", Password: "p"},
	}
	cfg.Services["web"] = config.ServiceDef{
		Image: "ghcr.io/org/web:v1", // ghcr.io NOT declared
		Build: &config.BuildSpec{Context: "./"},
	}
	assertValidationError(t, cfg, "ghcr.io")
}

func TestValidateConfig_BuildValid(t *testing.T) {
	cfg := validCfgForTest()
	cfg.Registry = map[string]config.RegistryDef{
		"ghcr.io": {Username: "u", Password: "p"},
	}
	cfg.Services["web"] = config.ServiceDef{
		Image: "ghcr.io/org/web:v1",
		Build: &config.BuildSpec{Context: "./"},
	}
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("expected valid, got: %v", err)
	}
}

// validCfgForTest is a minimal valid AppConfig that subsequent tests
// mutate. Copied here because the existing helpers inline their own
// fixtures.
func validCfgForTest() *config.AppConfig {
	return &config.AppConfig{
		App: "myapp", Env: "prod",
		Providers: config.ProvidersDef{Infra: "hetzner"},
		Servers: map[string]config.ServerDef{
			"master": {Type: "cax11", Region: "nbg1", Role: "master"},
		},
		Services: map[string]config.ServiceDef{},
	}
}

// ── imageRegistryHost unit coverage ───────────────────────────────────────

func TestImageRegistryHost(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"ghcr.io/org/app:v1", "ghcr.io"},
		{"docker.io/library/nginx", "docker.io"},
		{"registry.example.com:5000/foo:v1", "registry.example.com:5000"},
		{"localhost/foo:v1", "localhost"},
		{"nginx", ""},
		{"alpine:3.19", ""},
		{"org/repo:tag", ""}, // org namespace, not a host
		{"", ""},
	}
	for _, tt := range tests {
		if got := imageRegistryHost(tt.in); got != tt.want {
			t.Errorf("imageRegistryHost(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// ── providers.secrets validation ──────────────────────────────────────────

func TestValidateConfig_SecretsValid(t *testing.T) {
	for _, kind := range []string{"doppler", "awssm", "infisical"} {
		cfg := validCfg()
		cfg.Providers.Secrets = &config.SecretsDef{Kind: kind}
		if err := ValidateConfig(cfg); err != nil {
			t.Errorf("kind %q: unexpected error: %v", kind, err)
		}
	}
}

func TestValidateConfig_SecretsMissingKind(t *testing.T) {
	cfg := validCfg()
	cfg.Providers.Secrets = &config.SecretsDef{}
	assertValidationError(t, cfg, "providers.secrets.kind is required")
}

func TestValidateConfig_SecretsUnknownKind(t *testing.T) {
	cfg := validCfg()
	cfg.Providers.Secrets = &config.SecretsDef{Kind: "vault"}
	assertValidationError(t, cfg, "not supported")
}

// ── providers.tunnel validation ──────────────────────────────────────────

func TestValidateConfig_TunnelValid(t *testing.T) {
	for _, kind := range []string{"cloudflare", "ngrok"} {
		cfg := validCfgForTest()
		cfg.Providers.Tunnel = kind
		cfg.Providers.DNS = "cloudflare"
		cfg.Services["web"] = config.ServiceDef{Image: "nginx", Port: 80}
		cfg.Domains = map[string][]string{"web": {"api.myapp.com"}}
		if err := ValidateConfig(cfg); err != nil {
			t.Errorf("tunnel %q: unexpected error: %v", kind, err)
		}
	}
}

func TestValidateConfig_TunnelUnknownKind(t *testing.T) {
	cfg := validCfgForTest()
	cfg.Providers.Tunnel = "tailscale"
	cfg.Providers.DNS = "cloudflare"
	cfg.Services["web"] = config.ServiceDef{Image: "nginx", Port: 80}
	cfg.Domains = map[string][]string{"web": {"api.myapp.com"}}
	assertValidationError(t, cfg, "not supported")
}

func TestValidateConfig_TunnelRequiresDNS(t *testing.T) {
	cfg := validCfgForTest()
	cfg.Providers.Tunnel = "cloudflare"
	cfg.Providers.DNS = ""
	cfg.Services["web"] = config.ServiceDef{Image: "nginx", Port: 80}
	cfg.Domains = map[string][]string{"web": {"api.myapp.com"}}
	assertValidationError(t, cfg, "DNS provider is required")
}

func TestValidateConfig_TunnelRequiresDomains(t *testing.T) {
	cfg := validCfgForTest()
	cfg.Providers.Tunnel = "cloudflare"
	cfg.Providers.DNS = "cloudflare"
	assertValidationError(t, cfg, "at least one domain is required")
}

// ── providers.build validation ───────────────────────────────────────────

// TestValidateConfig_BuildUnset_OK locks the default: omitting providers.build
// is valid. The in-process path (reconcile.Deploy) runs as it always has.
func TestValidateConfig_BuildUnset_OK(t *testing.T) {
	cfg := validCfgForTest()
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("empty providers.build must be valid, got: %v", err)
	}
}

// TestValidateConfig_BuildLocal_OK locks the explicit default: providers.build:
// local is accepted and requires no builders (RequiresBuilders=false).
func TestValidateConfig_BuildLocal_OK(t *testing.T) {
	cfg := validCfgForTest()
	cfg.Providers.Build = "local"
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("providers.build: local must be valid, got: %v", err)
	}
}

// TestValidateConfig_BuildUnknownKind_Errors verifies we surface unregistered
// build providers at validate time — before any credential or infra work.
// Typos like `providers.build: loocal` must fail loudly.
func TestValidateConfig_BuildUnknownKind_Errors(t *testing.T) {
	cfg := validCfgForTest()
	cfg.Providers.Build = "loocal"
	assertValidationError(t, cfg, "unsupported build provider")
}

// TestValidateConfig_BuildLocal_WithBuilderServer_Errors locks R1 in the
// negative direction: `local` does NOT use builders, so declaring a
// role: builder server alongside `local` is a misconfiguration — the
// builder would sit idle. The provider-block validation fires before the
// server-role enum check, so the operator gets the actionable error
// (name the alternative build providers) instead of a generic "unknown role".
func TestValidateConfig_BuildLocal_WithBuilderServer_Errors(t *testing.T) {
	cfg := validCfgForTest()
	cfg.Providers.Build = "local"
	cfg.Servers["builder-1"] = config.ServerDef{Type: "cx23", Region: "fsn1", Role: "builder"}
	assertValidationError(t, cfg, "does not use builder servers")
}

// TestValidateConfig_BuildUnsetWithBuilderServer_Errors mirrors the local
// case for an unset build provider. Unset defaults to "local", so a stray
// role: builder server is just as wrong. The uniform "resolve to local"
// path means the error message names "local" explicitly — no special case.
func TestValidateConfig_BuildUnsetWithBuilderServer_Errors(t *testing.T) {
	cfg := validCfgForTest()
	cfg.Providers.Build = ""
	cfg.Servers["builder-1"] = config.ServerDef{Type: "cx23", Region: "fsn1", Role: "builder"}
	assertValidationError(t, cfg, "does not use builder servers")
}

// ── role: builder enum acceptance ────────────────────────────────────────

// TestValidateConfig_RoleBuilder_AcceptedUnderRequiresBuildersProvider covers
// the role-enum acceptance of "builder" with a synthetic test-fixture
// RequiresBuilders=true BuildProvider. The real `ssh` provider is exercised
// separately (see the TestValidateConfig_BuildSSH_* tests below) — keeping
// this fixture-based test proves the code path is generic for any
// RequiresBuilders=true provider (ssh, daytona), not hard-wired to one
// specific implementation.
func TestValidateConfig_RoleBuilder_AcceptedUnderRequiresBuildersProvider(t *testing.T) {
	registerTestBuildProvider(t, "testbuilder", provider.BuildCapability{RequiresBuilders: true})
	cfg := validCfgForTest()
	cfg.Providers.Build = "testbuilder"
	cfg.Servers["builder-1"] = config.ServerDef{Type: "cx23", Region: "fsn1", Role: "builder"}
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("role: builder must be accepted under a RequiresBuilders=true build provider, got: %v", err)
	}
}

// TestValidateConfig_RoleUnknown_Errors locks the role enum's rejection of
// typos. The error message names all three valid roles so the operator
// knows builder is a first-class option, not an internal-only role.
func TestValidateConfig_RoleUnknown_Errors(t *testing.T) {
	cfg := validCfgForTest()
	cfg.Servers["bad"] = config.ServerDef{Type: "cx23", Region: "fsn1", Role: "boss"}
	assertValidationError(t, cfg, "must be master, worker, or builder")
}

// TestValidateConfig_RequiresBuildersProvider_NoBuilderServer_Errors locks
// R1 in the positive direction: a build provider that declares
// RequiresBuilders=true must be matched by ≥1 role: builder server. Same
// test-fixture build provider as above.
func TestValidateConfig_RequiresBuildersProvider_NoBuilderServer_Errors(t *testing.T) {
	registerTestBuildProvider(t, "testbuilder2", provider.BuildCapability{RequiresBuilders: true})
	cfg := validCfgForTest()
	cfg.Providers.Build = "testbuilder2"
	assertValidationError(t, cfg, "requires at least one server with role: builder")
}

// ── Real `ssh` build provider matrix ─────────────────────────────────────
//
// The R1 tests above use a synthetic `testbuilder*` fixture to prove the
// validation code path is generic. The two tests below lock the actual
// `ssh` provider's registration-time capability bits by running the real
// registered entry through ValidateConfig. If someone ever flips
// `RequiresBuilders` on the ssh provider, these tests fail — the
// synthetic-fixture tests would still pass because they declare the bits
// inline.

// TestValidateConfig_BuildSSH_NoBuilderServer_Errors locks the real ssh
// provider's R1 positive direction: ssh requires ≥1 role: builder server.
// If a future edit drops RequiresBuilders to false this fails loudly.
func TestValidateConfig_BuildSSH_NoBuilderServer_Errors(t *testing.T) {
	cfg := validCfgForTest()
	cfg.Providers.Build = "ssh"
	// No builder server declared — the default validCfg only has a master.
	assertValidationError(t, cfg, "requires at least one server with role: builder")
}

// TestValidateConfig_BuildSSH_WithBuilderServer_OK locks the real ssh
// provider's R1 happy path: ssh + ≥1 role: builder server validates clean.
func TestValidateConfig_BuildSSH_WithBuilderServer_OK(t *testing.T) {
	cfg := validCfgForTest()
	cfg.Providers.Build = "ssh"
	cfg.Servers["builder-1"] = config.ServerDef{Type: "cx23", Region: "fsn1", Role: "builder"}
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("ssh + role: builder must validate clean, got: %v", err)
	}
}

// TestValidateConfig_BuildDaytona_WithBuilderServer_Errors locks the R1
// negative direction for daytona: RequiresBuilders=false, so pairing it
// with a role: builder server is a misconfiguration (idle infra).
func TestValidateConfig_BuildDaytona_WithBuilderServer_Errors(t *testing.T) {
	cfg := validCfgForTest()
	cfg.Providers.Build = "daytona"
	cfg.Servers["builder-1"] = config.ServerDef{Type: "cx23", Region: "fsn1", Role: "builder"}
	assertValidationError(t, cfg, "does not use builder servers")
}

// TestValidateConfig_BuildDaytona_NoBuilderServer_OK locks daytona's happy
// path: no builder servers declared, validates clean.
func TestValidateConfig_BuildDaytona_NoBuilderServer_OK(t *testing.T) {
	cfg := validCfgForTest()
	cfg.Providers.Build = "daytona"
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("daytona + no builder must validate clean, got: %v", err)
	}
}

// registerTestBuildProvider registers a throwaway BuildProvider in the
// provider registry under a unique name. Used by validator tests that need
// specific BuildCapability bits without pulling in a production provider.
// The factory returns a nil BuildProvider because the validator only
// queries GetBuildCapability — it never calls ResolveBuild.
func registerTestBuildProvider(_ *testing.T, name string, caps provider.BuildCapability) {
	provider.RegisterBuild(name, provider.CredentialSchema{}, caps, func(map[string]string) provider.BuildProvider {
		return nil
	})
}

// ── providers.ci validation ──────────────────────────────────────────────

// TestValidateConfig_CIUnset_OK locks the default: omitting providers.ci is
// valid. `nvoi ci init` is opt-in — a config that never sets ci must deploy
// exactly as it did before the feature landed.
func TestValidateConfig_CIUnset_OK(t *testing.T) {
	cfg := validCfgForTest()
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("empty providers.ci must be valid, got: %v", err)
	}
}

// TestValidateConfig_CIGitHub_OK locks the happy path for the only CI
// provider shipped today. If the github package's registration ever breaks
// this fails loudly before any test that depends on `ci init` running.
func TestValidateConfig_CIGitHub_OK(t *testing.T) {
	cfg := validCfgForTest()
	cfg.Providers.Ci = "github"
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("providers.ci: github must be valid, got: %v", err)
	}
}

// TestValidateConfig_CIUnknownKind_Errors surfaces typos at validate time —
// before any token is read, any secret synced, or any workflow committed.
// `providers.ci: githob` would otherwise silently pass the CLI layer and
// fail obscurely inside ResolveCI.
func TestValidateConfig_CIUnknownKind_Errors(t *testing.T) {
	cfg := validCfgForTest()
	cfg.Providers.Ci = "githob"
	assertValidationError(t, cfg, "providers.ci")
}
