package config

import (
	"testing"
)

func hetznerConfig() *Config {
	return &Config{
		Servers: map[string]Server{
			"master":   {Type: "cx23", Region: "fsn1"},
			"worker-1": {Type: "cx33", Region: "fsn1"},
		},
		Volumes: map[string]Volume{
			"meili-data": {Size: 20, Server: "master"},
			"pgdata":     {Size: 30, Server: "master"},
		},
		Build: map[string]Build{
			"web": {Source: "benbonnet/dummy-rails"},
		},
		Storage: map[string]Storage{
			"assets": {CORS: true},
		},
		Services: map[string]Service{
			"db": {
				Image:   "postgres:17",
				Volumes: []string{"pgdata:/var/lib/postgresql/data"},
				Env:     []string{"POSTGRES_USER", "POSTGRES_DB"},
				Secrets: []string{"POSTGRES_PASSWORD"},
			},
			"meilisearch": {
				Image:   "getmeili/meilisearch:latest",
				Volumes: []string{"meili-data:/meili_data"},
			},
			"web": {
				Build:    "web",
				Port:     80,
				Replicas: 2,
				Health:   "/up",
				Server:   "worker-1",
				Env:      []string{"RAILS_ENV=production", "POSTGRES_HOST=db", "POSTGRES_USER", "POSTGRES_DB"},
				Secrets:  []string{"POSTGRES_PASSWORD", "RAILS_MASTER_KEY"},
				Storage:  []string{"assets"},
			},
			"jobs": {
				Build:   "web",
				Command: "bin/jobs",
				Server:  "worker-1",
				Env:     []string{"RAILS_ENV=production", "POSTGRES_HOST=db", "POSTGRES_USER", "POSTGRES_DB"},
				Secrets: []string{"POSTGRES_PASSWORD", "RAILS_MASTER_KEY"},
			},
		},
		Domains: map[string]Domains{
			"web": {"final.nvoi.to"},
		},
	}
}

func hetznerEnv() map[string]string {
	return map[string]string{
		"POSTGRES_USER":     "myapp",
		"POSTGRES_DB":       "myapp_prod",
		"POSTGRES_PASSWORD": "s3cret",
		"RAILS_MASTER_KEY":  "abc123",
	}
}

func TestPlan_PhaseOrder(t *testing.T) {
	steps, err := Plan(hetznerConfig(), hetznerEnv())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	// Verify phase ordering: compute → volumes → build → secrets → storage → services → dns
	var kinds []StepKind
	for _, s := range steps {
		kinds = append(kinds, s.Kind)
	}

	phases := []StepKind{
		StepComputeSet, StepComputeSet,     // master, worker-1
		StepVolumeSet, StepVolumeSet,       // meili-data, pgdata (sorted)
		StepBuild,                          // web
		StepSecretSet, StepSecretSet,       // POSTGRES_PASSWORD, RAILS_MASTER_KEY (sorted)
		StepStorageSet,                     // assets
		StepServiceSet, StepServiceSet, StepServiceSet, StepServiceSet, // db, jobs, meilisearch, web (sorted)
		StepDNSSet, // web
	}

	if len(kinds) != len(phases) {
		t.Fatalf("got %d steps, want %d:\n  got:  %v\n  want: %v", len(kinds), len(phases), kinds, phases)
	}
	for i, want := range phases {
		if kinds[i] != want {
			t.Errorf("step[%d] = %s, want %s", i, kinds[i], want)
		}
	}
}

func TestPlan_ComputeMasterFirst(t *testing.T) {
	steps, err := Plan(hetznerConfig(), hetznerEnv())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	// First step is master (alphabetically first), no worker flag.
	s := steps[0]
	if s.Kind != StepComputeSet || s.Name != "master" {
		t.Errorf("step[0] = %s %s, want instance.set master", s.Kind, s.Name)
	}
	if _, ok := s.Params["worker"]; ok {
		t.Error("master should not have worker param")
	}

	// Second is worker-1, with worker=true.
	s = steps[1]
	if s.Kind != StepComputeSet || s.Name != "worker-1" {
		t.Errorf("step[1] = %s %s, want instance.set worker-1", s.Kind, s.Name)
	}
	if s.Params["worker"] != true {
		t.Error("worker-1 should have worker=true")
	}
}

func TestPlan_VolumeParams(t *testing.T) {
	steps, err := Plan(hetznerConfig(), hetznerEnv())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	vol := findStep(steps, StepVolumeSet, "pgdata")
	if vol == nil {
		t.Fatal("pgdata volume step not found")
	}
	if vol.Params["size"] != 30 {
		t.Errorf("pgdata size = %v", vol.Params["size"])
	}
	if vol.Params["server"] != "master" {
		t.Errorf("pgdata server = %v", vol.Params["server"])
	}
}

func TestPlan_BuildParams(t *testing.T) {
	steps, err := Plan(hetznerConfig(), hetznerEnv())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	b := findStep(steps, StepBuild, "web")
	if b == nil {
		t.Fatal("web build step not found")
	}
	if b.Params["source"] != "benbonnet/dummy-rails" {
		t.Errorf("build source = %v", b.Params["source"])
	}
}

func TestPlan_SecretsDeduplicated(t *testing.T) {
	steps, err := Plan(hetznerConfig(), hetznerEnv())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	// POSTGRES_PASSWORD is referenced by db, web, and jobs — should appear once.
	count := 0
	for _, s := range steps {
		if s.Kind == StepSecretSet && s.Name == "POSTGRES_PASSWORD" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("POSTGRES_PASSWORD steps = %d, want 1", count)
	}
}

func TestPlan_SecretsResolved(t *testing.T) {
	steps, err := Plan(hetznerConfig(), hetznerEnv())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	s := findStep(steps, StepSecretSet, "POSTGRES_PASSWORD")
	if s == nil {
		t.Fatal("POSTGRES_PASSWORD step not found")
	}
	if s.Params["value"] != "s3cret" {
		t.Errorf("secret value = %v, want s3cret", s.Params["value"])
	}
}

func TestPlan_StorageParams(t *testing.T) {
	steps, err := Plan(hetznerConfig(), hetznerEnv())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	s := findStep(steps, StepStorageSet, "assets")
	if s == nil {
		t.Fatal("assets storage step not found")
	}
	if s.Params["cors"] != true {
		t.Errorf("cors = %v", s.Params["cors"])
	}
}

func TestPlan_ServiceEnvResolved(t *testing.T) {
	steps, err := Plan(hetznerConfig(), hetznerEnv())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	// db service: env has bare keys (POSTGRES_USER, POSTGRES_DB) → resolved from env.
	db := findStep(steps, StepServiceSet, "db")
	if db == nil {
		t.Fatal("db service step not found")
	}
	envList := toStringSlice(db.Params["env"])
	assertContains(t, envList, "POSTGRES_USER=myapp")
	assertContains(t, envList, "POSTGRES_DB=myapp_prod")

	// web service: mix of literal and resolved.
	web := findStep(steps, StepServiceSet, "web")
	if web == nil {
		t.Fatal("web service step not found")
	}
	webEnv := toStringSlice(web.Params["env"])
	assertContains(t, webEnv, "RAILS_ENV=production")
	assertContains(t, webEnv, "POSTGRES_HOST=db")
	assertContains(t, webEnv, "POSTGRES_USER=myapp")
}

func TestPlan_ServiceBuildRef(t *testing.T) {
	steps, err := Plan(hetznerConfig(), hetznerEnv())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	web := findStep(steps, StepServiceSet, "web")
	if web == nil {
		t.Fatal("web service step not found")
	}
	if web.Params["build"] != "web" {
		t.Errorf("web build = %v", web.Params["build"])
	}
	if web.Params["port"] != 80 {
		t.Errorf("web port = %v", web.Params["port"])
	}
	if web.Params["replicas"] != 2 {
		t.Errorf("web replicas = %v", web.Params["replicas"])
	}
}

func TestPlan_DNSParams(t *testing.T) {
	steps, err := Plan(hetznerConfig(), hetznerEnv())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	dns := findStep(steps, StepDNSSet, "web")
	if dns == nil {
		t.Fatal("web dns step not found")
	}
	domains := toStringSlice(dns.Params["domains"])
	if len(domains) != 1 || domains[0] != "final.nvoi.to" {
		t.Errorf("domains = %v", domains)
	}
}

func TestPlan_MinimalConfig(t *testing.T) {
	cfg := &Config{
		Servers: map[string]Server{
			"master": {Type: "t3.medium", Region: "eu-west-3"},
		},
		Services: map[string]Service{
			"web": {Image: "nginx:latest", Port: 80},
		},
	}
	steps, err := Plan(cfg, nil)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	// compute + service = 2 steps.
	if len(steps) != 2 {
		t.Fatalf("got %d steps, want 2: %v", len(steps), steps)
	}
	if steps[0].Kind != StepComputeSet {
		t.Errorf("step[0] = %s", steps[0].Kind)
	}
	if steps[1].Kind != StepServiceSet {
		t.Errorf("step[1] = %s", steps[1].Kind)
	}
}

func TestPlan_SecretMissingFromEnv(t *testing.T) {
	cfg := &Config{
		Servers:  map[string]Server{"master": {Type: "cx23", Region: "fsn1"}},
		Services: map[string]Service{"web": {Image: "nginx", Secrets: []string{"MISSING_KEY"}}},
	}
	_, err := Plan(cfg, map[string]string{})
	if err == nil {
		t.Fatal("expected error for missing secret")
	}
}

func TestPlan_EnvMissingFromEnv(t *testing.T) {
	cfg := &Config{
		Servers:  map[string]Server{"master": {Type: "cx23", Region: "fsn1"}},
		Services: map[string]Service{"web": {Image: "nginx", Env: []string{"MISSING_KEY"}}},
	}
	_, err := Plan(cfg, map[string]string{})
	if err == nil {
		t.Fatal("expected error for missing env key")
	}
}

func TestPlan_InvalidConfig(t *testing.T) {
	cfg := &Config{} // no servers, no services
	_, err := Plan(cfg, nil)
	if err == nil {
		t.Fatal("expected validation error")
	}
}

// ── helpers ────────────────────────────────────────────────────────────────────

func findStep(steps []Step, kind StepKind, name string) *Step {
	for i := range steps {
		if steps[i].Kind == kind && steps[i].Name == name {
			return &steps[i]
		}
	}
	return nil
}

func toStringSlice(v any) []string {
	if v == nil {
		return nil
	}
	switch val := v.(type) {
	case []string:
		return val
	case []any:
		var out []string
		for _, item := range val {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func assertContains(t *testing.T, list []string, want string) {
	t.Helper()
	for _, s := range list {
		if s == want {
			return
		}
	}
	t.Errorf("list %v does not contain %q", list, want)
}
