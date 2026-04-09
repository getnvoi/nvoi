package plan

import (
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/internal/api/config"
	"github.com/google/go-cmp/cmp"
)

func managedConfig() *config.Config {
	return &config.Config{
		Servers: map[string]config.Server{
			"master": {Type: "cx23", Region: "fsn1", Role: "master"},
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
	"POSTGRES_USER":     "a1b2c3d4",
	"POSTGRES_DB":       "e5f6a7b8",
	"NVOI_AGENT_TOKEN":  "tok123",
}

func TestResolveDeploymentSteps_PostgresAndAgent(t *testing.T) {
	cfg := managedConfig()
	cfg.Services["db"] = config.Service{Managed: "postgres"}
	cfg.Services["coder"] = config.Service{Managed: "claude"}
	cfg.Services["web"] = config.Service{Workload: config.Workload{Image: "nginx"}, Port: 80, Uses: []string{"db", "coder"}}

	got, err := ResolveDeploymentSteps(cfg, nil, managedEnv)
	if err != nil {
		t.Fatalf("ResolveDeploymentSteps() error = %v", err)
	}

	// Managed services still in config (Build skips them via owned set).
	if _, ok := got.Config.Services["db"]; !ok {
		t.Fatal("Config should still contain managed service db")
	}

	// Config is returned unmodified — uses: still present, secrets unchanged.
	web := got.Config.Services["web"]
	if len(web.Uses) == 0 {
		t.Fatal("web.Uses should still be present (config not mutated)")
	}

	// Check step sequence.
	gotSteps := stepKindNames(got.Steps)
	wantSteps := []string{
		"instance.set:master",
		"secret.set:NVOI_AGENT_TOKEN_CODER",
		"secret.set:AGENT_CODER_HOST", "secret.set:AGENT_CODER_PORT",
		"secret.set:AGENT_CODER_TOKEN", "secret.set:AGENT_CODER_URL",
		"secret.set:POSTGRES_PASSWORD_DB",
		"secret.set:POSTGRES_USER_DB", "secret.set:POSTGRES_DB_DB",
		"secret.set:DATABASE_DB_HOST", "secret.set:DATABASE_DB_PORT",
		"secret.set:DATABASE_DB_USER", "secret.set:DATABASE_DB_PASSWORD",
		"secret.set:DATABASE_DB_NAME", "secret.set:DATABASE_DB_URL",
		"volume.set:coder-data", "volume.set:db-data",
		"service.set:coder", "service.set:db",
		"service.set:web",
	}
	if diff := cmp.Diff(wantSteps, gotSteps); diff != "" {
		t.Fatalf("Steps (-want +got):\n%s", diff)
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
	cfg.Services["web"] = config.Service{Workload: config.Workload{Image: "nginx"}, Port: 80, Uses: []string{"db"}}

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
	cfg.Services["web"] = config.Service{Workload: config.Workload{Image: "nginx"}, Port: 80, Uses: []string{"db"}}

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
	if diff := cmp.Diff(steps1, steps2); diff != "" {
		t.Fatalf("not deterministic (-want +got):\n%s", diff)
	}
}

func TestResolveDeploymentSteps_WebStepGetsInjectedSecrets(t *testing.T) {
	cfg := managedConfig()
	cfg.Services["db"] = config.Service{Managed: "postgres"}
	cfg.Services["web"] = config.Service{Workload: config.Workload{Image: "nginx"}, Port: 80, Uses: []string{"db"}}

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
