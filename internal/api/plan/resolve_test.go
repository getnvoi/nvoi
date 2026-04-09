package plan

import (
	"reflect"
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/internal/api/config"
)

func managedConfig() *config.Config {
	return &config.Config{
		Servers: map[string]config.Server{
			"master": {Type: "cx23", Region: "fsn1"},
		},
		Volumes:  map[string]config.Volume{},
		Storage:  map[string]config.Storage{},
		Build:    map[string]config.Build{},
		Services: map[string]config.Service{},
		Domains:  map[string]config.Domains{},
	}
}

var managedEnv = map[string]string{
	"POSTGRES_PASSWORD": "s3cret",
	"NVOI_AGENT_TOKEN":  "tok123",
}

func TestResolveDeploymentSteps_PostgresAndAgent(t *testing.T) {
	cfg := managedConfig()
	cfg.Services["db"] = config.Service{Managed: "postgres"}
	cfg.Services["coder"] = config.Service{Managed: "claude"}
	cfg.Services["web"] = config.Service{Image: "nginx", Port: 80, Uses: []string{"db", "coder"}}

	got, err := ResolveDeploymentSteps(cfg, nil, managedEnv)
	if err != nil {
		t.Fatalf("ResolveDeploymentSteps() error = %v", err)
	}

	// Managed roots excluded from stripped config.
	if _, ok := got.Config.Services["db"]; ok {
		t.Fatal("Config should exclude managed service db")
	}
	if _, ok := got.Config.Services["coder"]; ok {
		t.Fatal("Config should exclude managed service coder")
	}

	// Consuming workload gets injected secrets, uses: cleared.
	web := got.Config.Services["web"]
	if len(web.Uses) != 0 {
		t.Fatalf("web.Uses = %v, want nil after resolution", web.Uses)
	}
	wantSecrets := []string{
		"DATABASE_DB_HOST", "DATABASE_DB_NAME", "DATABASE_DB_PASSWORD",
		"DATABASE_DB_PORT", "DATABASE_DB_URL", "DATABASE_DB_USER",
		"AGENT_CODER_HOST", "AGENT_CODER_PORT", "AGENT_CODER_TOKEN", "AGENT_CODER_URL",
	}
	if !reflect.DeepEqual(web.Secrets, wantSecrets) {
		t.Fatalf("web.Secrets = %v, want %v", web.Secrets, wantSecrets)
	}

	// Check step sequence.
	gotSteps := stepKindNames(got.Steps)
	wantSteps := []string{
		"instance.set:master",
		"secret.set:NVOI_AGENT_TOKEN_CODER",
		"secret.set:AGENT_CODER_HOST", "secret.set:AGENT_CODER_PORT",
		"secret.set:AGENT_CODER_TOKEN", "secret.set:AGENT_CODER_URL",
		"secret.set:POSTGRES_PASSWORD_DB",
		"secret.set:DATABASE_DB_HOST", "secret.set:DATABASE_DB_PORT",
		"secret.set:DATABASE_DB_USER", "secret.set:DATABASE_DB_PASSWORD",
		"secret.set:DATABASE_DB_NAME", "secret.set:DATABASE_DB_URL",
		"storage.set:db-backups",
		"volume.set:coder-data", "volume.set:db-data",
		"service.set:coder", "service.set:db", "cron.set:db-backup",
		"service.set:web",
	}
	if !reflect.DeepEqual(gotSteps, wantSteps) {
		t.Fatalf("Steps =\n  %v\nwant\n  %v", gotSteps, wantSteps)
	}
}

func TestResolveDeploymentSteps_MissingCredential(t *testing.T) {
	cfg := managedConfig()
	cfg.Services["db"] = config.Service{Managed: "postgres"}

	_, err := ResolveDeploymentSteps(cfg, nil, map[string]string{})
	if err == nil {
		t.Fatal("expected error for missing POSTGRES_PASSWORD")
	}
	if !strings.Contains(err.Error(), "POSTGRES_PASSWORD") {
		t.Fatalf("error should mention POSTGRES_PASSWORD, got: %v", err)
	}
}

func TestResolveDeploymentSteps_NoDuplicates(t *testing.T) {
	cfg := managedConfig()
	cfg.Services["db"] = config.Service{Managed: "postgres"}
	cfg.Services["web"] = config.Service{Image: "nginx", Port: 80, Uses: []string{"db"}}

	got, err := ResolveDeploymentSteps(cfg, nil, managedEnv)
	if err != nil {
		t.Fatalf("ResolveDeploymentSteps() error = %v", err)
	}

	counts := map[string]int{}
	for _, step := range got.Steps {
		key := string(step.Kind) + ":" + step.Name
		counts[key]++
	}
	for key, count := range counts {
		if count > 1 {
			t.Errorf("duplicate step %s (count=%d)", key, count)
		}
	}
}

func TestResolveDeploymentSteps_Deterministic(t *testing.T) {
	cfg := managedConfig()
	cfg.Services["db"] = config.Service{Managed: "postgres"}
	cfg.Services["web"] = config.Service{Image: "nginx", Port: 80, Uses: []string{"db"}}

	got1, err := ResolveDeploymentSteps(cfg, nil, managedEnv)
	if err != nil {
		t.Fatalf("first call error = %v", err)
	}
	got2, err := ResolveDeploymentSteps(cfg, nil, managedEnv)
	if err != nil {
		t.Fatalf("second call error = %v", err)
	}

	steps1 := stepKindNames(got1.Steps)
	steps2 := stepKindNames(got2.Steps)
	if !reflect.DeepEqual(steps1, steps2) {
		t.Fatalf("not deterministic:\n  first  = %v\n  second = %v", steps1, steps2)
	}
}

func TestResolveDeploymentSteps_WebStepGetsInjectedSecrets(t *testing.T) {
	cfg := managedConfig()
	cfg.Services["db"] = config.Service{Managed: "postgres"}
	cfg.Services["web"] = config.Service{Image: "nginx", Port: 80, Uses: []string{"db"}}

	got, err := ResolveDeploymentSteps(cfg, nil, managedEnv)
	if err != nil {
		t.Fatalf("ResolveDeploymentSteps() error = %v", err)
	}

	var webStep *Step
	for i, step := range got.Steps {
		if step.Kind == StepServiceSet && step.Name == "web" {
			webStep = &got.Steps[i]
			break
		}
	}
	if webStep == nil {
		t.Fatal("web service.set step not found")
	}

	secrets := toStringSlice(webStep.Params["secrets"])
	for _, want := range []string{"DATABASE_DB_HOST", "DATABASE_DB_PASSWORD", "DATABASE_DB_URL"} {
		found := false
		for _, s := range secrets {
			if s == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("web service.set secrets missing %q, got %v", want, secrets)
		}
	}
}

// ── helpers ────────────────────────────────────────────────────────────────────

func stepKindNames(steps []Step) []string {
	names := make([]string, 0, len(steps))
	for _, step := range steps {
		names = append(names, string(step.Kind)+":"+step.Name)
	}
	return names
}
