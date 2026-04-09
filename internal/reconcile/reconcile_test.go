package reconcile

import (
	"context"
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/internal/testutil"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
	"github.com/spf13/viper"
)

// ── Test helpers ────────────────────────────────────────────────────────────

func init() {
	provider.RegisterCompute("test-compute", provider.CredentialSchema{Name: "test-compute"}, func(creds map[string]string) provider.ComputeProvider {
		return &testutil.MockCompute{
			Servers: []*provider.Server{{
				ID: "1", Name: "nvoi-myapp-prod-master", Status: "running",
				IPv4: "1.2.3.4", PrivateIP: "10.0.1.1",
			}},
		}
	})
}

func testDC(ssh *testutil.MockSSH) *DeployContext {
	sshKey, _, _ := utils.GenerateEd25519Key()
	return &DeployContext{
		Cluster: app.Cluster{
			AppName: "myapp", Env: "prod",
			Provider: "test-compute", Credentials: map[string]string{},
			SSHKey: sshKey,
			Output: &testutil.MockOutput{},
			SSHFunc: func(ctx context.Context, addr string) (utils.SSHClient, error) {
				return ssh, nil
			},
		},
	}
}

func testViper(kvs ...string) *viper.Viper {
	v := viper.New()
	for i := 0; i+1 < len(kvs); i += 2 {
		v.Set(kvs[i], kvs[i+1])
	}
	return v
}

func sshWithKube() *testutil.MockSSH {
	return &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "create namespace", Result: testutil.MockResult{}},
			{Prefix: "apply --server-side", Result: testutil.MockResult{}},
			{Prefix: "get secret", Result: testutil.MockResult{Output: []byte("'{}'")}},
			{Prefix: "create secret", Result: testutil.MockResult{}},
			{Prefix: "patch secret", Result: testutil.MockResult{}},
			{Prefix: "delete deployment", Result: testutil.MockResult{}},
			{Prefix: "delete statefulset", Result: testutil.MockResult{}},
			{Prefix: "delete service/", Result: testutil.MockResult{}},
			{Prefix: "delete cronjob", Result: testutil.MockResult{}},
			{Prefix: "rollout status", Result: testutil.MockResult{}},
			{Prefix: "get pods", Result: testutil.MockResult{Output: []byte("'web-abc123'")}},
			{Prefix: "get deploy", Result: testutil.MockResult{Output: []byte(`{"items":[]}`)}},
			{Prefix: "get statefulset", Result: testutil.MockResult{Output: []byte(`{"items":[]}`)}},
		},
	}
}

// ── Test config helper ──────────────────────────────────────────────────────

func validCfg() *AppConfig {
	return &AppConfig{
		App: "myapp", Env: "prod",
		Providers: ProvidersDef{Compute: "test-compute"},
		Servers:   map[string]ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services:  map[string]ServiceDef{"web": {Image: "nginx"}},
	}
}

// ── Validation tests ────────────────────────────────────────────────────────

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
	cfg.Services["web"] = ServiceDef{Image: "nginx"} // no port
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

// ── ResolveServer tests ─────────────────────────────────────────────────────

func TestResolveServer_ExplicitServer(t *testing.T) {
	cfg := &AppConfig{}
	if got := ResolveServer(cfg, "worker-1", nil); got != "worker-1" {
		t.Errorf("expected worker-1, got %q", got)
	}
}

func TestResolveServer_AutoPinFromVolume(t *testing.T) {
	cfg := &AppConfig{
		Volumes: map[string]VolumeDef{"pgdata": {Size: 20, Server: "master"}},
	}
	got := ResolveServer(cfg, "", []string{"pgdata:/data"})
	if got != "master" {
		t.Errorf("expected master (auto-pinned from volume), got %q", got)
	}
}

func TestResolveServer_NoServerNoVolume(t *testing.T) {
	cfg := &AppConfig{}
	if got := ResolveServer(cfg, "", nil); got != "" {
		t.Errorf("expected empty (default to master in kube), got %q", got)
	}
}

// ── Deploy integration tests ────────────────────────────────────────────────

func TestReconcileSecrets_MissingSecret_Error(t *testing.T) {
	dc := testDC(sshWithKube())
	cfg := &AppConfig{Secrets: []string{"MISSING_KEY"}}
	err := Secrets(context.Background(), dc, nil, cfg, testViper())
	if err == nil {
		t.Fatal("expected error for missing secret")
	}
	if !strings.Contains(err.Error(), "MISSING_KEY") {
		t.Errorf("error should mention MISSING_KEY, got: %s", err)
	}
}

func TestReconcileSecrets_FromViper(t *testing.T) {
	mock := sshWithKube()
	dc := testDC(mock)
	cfg := &AppConfig{Secrets: []string{"DB_PASS"}}
	v := testViper("DB_PASS", "s3cret")
	err := Secrets(context.Background(), dc, nil, cfg, v)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// SecretSet uploads a JSON patch file with the secret value.
	found := false
	for _, u := range mock.Uploads {
		if strings.Contains(string(u.Content), "DB_PASS") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected uploaded patch containing DB_PASS")
	}
}

func TestReconcileSecrets_DeletesOrphans(t *testing.T) {
	mock := sshWithKube()
	dc := testDC(mock)
	live := &LiveState{Secrets: []string{"DB_PASS", "OLD_KEY"}}
	cfg := &AppConfig{Secrets: []string{"DB_PASS"}}
	v := testViper("DB_PASS", "s3cret")
	err := Secrets(context.Background(), dc, live, cfg, v)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ── DescribeLive tests ──────────────────────────────────────────────────────

func TestDescribeLive_ReturnsNilOnError(t *testing.T) {
	dc := &DeployContext{
		Cluster: app.Cluster{
			AppName: "myapp", Env: "prod",
			Provider: "nonexistent",
			Output:   &testutil.MockOutput{},
		},
	}
	live := DescribeLive(context.Background(), dc)
	if live != nil {
		t.Errorf("expected nil on error, got %+v", live)
	}
}

// ── SplitServers tests ──────────────────────────────────────────────────────

func TestSplitServers_MasterFirst(t *testing.T) {
	servers := map[string]ServerDef{
		"worker-1": {Type: "cx33", Region: "fsn1", Role: "worker"},
		"master":   {Type: "cx23", Region: "fsn1", Role: "master"},
		"worker-2": {Type: "cx33", Region: "fsn1", Role: "worker"},
	}
	masters, workers := SplitServers(servers)
	if len(masters) != 1 || masters[0].Name != "master" {
		t.Errorf("expected 1 master, got %v", masters)
	}
	if len(workers) != 2 {
		t.Errorf("expected 2 workers, got %d", len(workers))
	}
	if workers[0].Name != "worker-1" || workers[1].Name != "worker-2" {
		t.Errorf("workers should be sorted, got %v", workers)
	}
}

// ── ToSet tests ─────────────────────────────────────────────────────────────

func TestToSet(t *testing.T) {
	s := toSet([]string{"a", "b", "c"})
	if !s["a"] || !s["b"] || !s["c"] {
		t.Errorf("expected a,b,c in set")
	}
	if s["d"] {
		t.Errorf("d should not be in set")
	}
}

// ── BuildTargetStrings tests ────────────────────────────────────────────────

func TestBuildTargetStrings(t *testing.T) {
	build := map[string]string{"web": "org/repo", "api": "org/api"}
	targets := buildTargetStrings(build)
	if len(targets) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(targets))
	}
	// Sorted by key
	if targets[0] != "api:org/api" {
		t.Errorf("targets[0] = %q, want api:org/api", targets[0])
	}
	if targets[1] != "web:org/repo" {
		t.Errorf("targets[1] = %q, want web:org/repo", targets[1])
	}
}

// ── Schema tests ────────────────────────────────────────────────────────────

func TestParseAppConfig_InvalidYAML(t *testing.T) {
	_, err := ParseAppConfig([]byte("not: [valid: yaml"))
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestParseAppConfig_Valid(t *testing.T) {
	cfg, err := ParseAppConfig([]byte("app: test\nenv: prod\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.App != "test" || cfg.Env != "prod" {
		t.Errorf("got app=%q env=%q", cfg.App, cfg.Env)
	}
}

// ── CopyMap tests ───────────────────────────────────────────────────────────

func TestCopyMap(t *testing.T) {
	orig := map[string]string{"a": "1", "b": "2"}
	cp := copyMap(orig)
	cp["c"] = "3"
	if _, ok := orig["c"]; ok {
		t.Error("copy should not affect original")
	}
	if cp["a"] != "1" || cp["b"] != "2" {
		t.Error("copy should have original values")
	}
}
