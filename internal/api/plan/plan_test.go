package plan

import (
	"testing"

	"github.com/getnvoi/nvoi/internal/api/config"
)

func hetznerConfig() *Cfg {
	return &Cfg{
		Servers: map[string]config.Server{
			"master":   {Type: "cx23", Region: "fsn1"},
			"worker-1": {Type: "cx33", Region: "fsn1"},
		},
		Volumes: map[string]config.Volume{
			"meili-data": {Size: 20, Server: "master"},
			"pgdata":     {Size: 30, Server: "master"},
		},
		Build: map[string]config.Build{
			"web": {Source: "benbonnet/dummy-rails"},
		},
		Storage: map[string]config.Storage{
			"assets": {CORS: true},
		},
		Services: map[string]config.Service{
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
		Domains: map[string]config.Domains{
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
	steps, err := Build(nil, hetznerConfig(), hetznerEnv())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	// Verify phase ordering: compute → volumes → build → secrets → storage → services → dns
	var kinds []StepKind
	for _, s := range steps {
		kinds = append(kinds, s.Kind)
	}

	phases := []StepKind{
		StepComputeSet, StepComputeSet, // master, worker-1
		StepVolumeSet, StepVolumeSet, // meili-data, pgdata (sorted)
		StepBuild,                    // web
		StepSecretSet, StepSecretSet, // POSTGRES_PASSWORD, RAILS_MASTER_KEY (sorted)
		StepStorageSet,                                                 // assets
		StepServiceSet, StepServiceSet, StepServiceSet, StepServiceSet, // db, jobs, meilisearch, web (sorted)
		StepDNSSet,     // web
		StepIngressSet, // single caddy deploy
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

func TestPlan_IngressConfigProvidedTLSResolvedFromEnv(t *testing.T) {
	cfg := hetznerConfig()
	cfg.Firewall = &config.FirewallConfig{Preset: "default"}
	cfg.Ingress = &config.IngressConfig{
		Exposure: "direct",
		TLS: &config.IngressTLSConfig{
			Mode: "provided",
			Cert: "TLS_CERT_PEM",
			Key:  "TLS_KEY_PEM",
		},
	}
	env := hetznerEnv()
	env["TLS_CERT_PEM"] = "cert-pem"
	env["TLS_KEY_PEM"] = "key-pem"

	steps, err := Build(nil, cfg, env)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	ingress := findStep(steps, StepIngressSet, "web")
	if ingress == nil {
		t.Fatal("expected ingress.apply step")
	}
	if ingress.Params["tls_mode"] != "provided" {
		t.Fatalf("tls_mode = %v, want provided", ingress.Params["tls_mode"])
	}
	if ingress.Params["cert_pem"] != "cert-pem" {
		t.Fatalf("cert_pem = %v, want cert-pem", ingress.Params["cert_pem"])
	}
	if ingress.Params["key_pem"] != "key-pem" {
		t.Fatalf("key_pem = %v, want key-pem", ingress.Params["key_pem"])
	}
}

func TestPlan_EdgeOverlayCarriesProviderAndProxyToIngressAndDNS(t *testing.T) {
	cfg := hetznerConfig()
	cfg.Firewall = &config.FirewallConfig{Preset: "cloudflare"}
	cfg.Ingress = &config.IngressConfig{
		Exposure: "edge_proxied",
		TLS: &config.IngressTLSConfig{
			Mode: "edge_origin",
		},
		Edge: &config.IngressEdgeConfig{
			Provider: "cloudflare",
		},
	}

	steps, err := Build(nil, cfg, hetznerEnv())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	dns := findStep(steps, StepDNSSet, "web")
	if dns == nil || dns.Params["edge_proxied"] != true {
		t.Fatalf("expected proxied dns.set, got %+v", dns)
	}

	ingress := findStep(steps, StepIngressSet, "web")
	if ingress == nil {
		t.Fatal("expected ingress.apply step")
	}
	if ingress.Params["exposure"] != "edge_proxied" {
		t.Fatalf("exposure = %v, want edge_proxied", ingress.Params["exposure"])
	}
	if ingress.Params["tls_mode"] != "edge_origin" {
		t.Fatalf("tls_mode = %v, want edge_origin", ingress.Params["tls_mode"])
	}
	if ingress.Params["edge_provider"] != "cloudflare" {
		t.Fatalf("edge_provider = %v, want cloudflare", ingress.Params["edge_provider"])
	}
}

func TestPlan_ComputeMasterFirst(t *testing.T) {
	steps, err := Build(nil, hetznerConfig(), hetznerEnv())
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
	steps, err := Build(nil, hetznerConfig(), hetznerEnv())
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
	steps, err := Build(nil, hetznerConfig(), hetznerEnv())
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

func TestStepKinds_IncludeCron(t *testing.T) {
	if StepCronSet != "cron.set" {
		t.Fatalf("StepCronSet = %q", StepCronSet)
	}
	if StepCronDelete != "cron.delete" {
		t.Fatalf("StepCronDelete = %q", StepCronDelete)
	}
}

func TestPlan_SecretsDeduplicated(t *testing.T) {
	steps, err := Build(nil, hetznerConfig(), hetznerEnv())
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
	steps, err := Build(nil, hetznerConfig(), hetznerEnv())
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
	steps, err := Build(nil, hetznerConfig(), hetznerEnv())
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
	steps, err := Build(nil, hetznerConfig(), hetznerEnv())
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
	steps, err := Build(nil, hetznerConfig(), hetznerEnv())
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
	steps, err := Build(nil, hetznerConfig(), hetznerEnv())
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
	cfg := &Cfg{
		Servers: map[string]config.Server{
			"master": {Type: "t3.medium", Region: "eu-west-3"},
		},
		Services: map[string]config.Service{
			"web": {Image: "nginx:latest", Port: 80},
		},
	}
	steps, err := Build(nil, cfg, nil)
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
	cfg := &Cfg{
		Servers:  map[string]config.Server{"master": {Type: "cx23", Region: "fsn1"}},
		Services: map[string]config.Service{"web": {Image: "nginx", Secrets: []string{"MISSING_KEY"}}},
	}
	_, err := Build(nil, cfg, map[string]string{})
	if err == nil {
		t.Fatal("expected error for missing secret")
	}
}

func TestPlan_EnvMissingFromEnv(t *testing.T) {
	cfg := &Cfg{
		Servers:  map[string]config.Server{"master": {Type: "cx23", Region: "fsn1"}},
		Services: map[string]config.Service{"web": {Image: "nginx", Env: []string{"MISSING_KEY"}}},
	}
	_, err := Build(nil, cfg, map[string]string{})
	if err == nil {
		t.Fatal("expected error for missing env key")
	}
}

func TestPlan_EmptyConfig(t *testing.T) {
	cfg := &Cfg{Servers: map[string]config.Server{}, Services: map[string]config.Service{}}
	steps, err := Build(nil, cfg, nil)
	if err != nil {
		t.Fatalf("empty config should not error: %v", err)
	}
	if len(steps) != 0 {
		t.Errorf("empty config should produce 0 steps, got %d", len(steps))
	}
}

// ── Diff (delete steps from Plan) ──────────────────────────────────────────────

func TestPlan_NilPrev_NoDeletes(t *testing.T) {
	steps, err := Build(nil, hetznerConfig(), hetznerEnv())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	for _, s := range steps {
		if isDelete(s.Kind) {
			t.Errorf("nil prev should produce no deletes, got %s %s", s.Kind, s.Name)
		}
	}
}

func TestPlan_IdenticalConfigs_NoDeletes(t *testing.T) {
	cfg := hetznerConfig()
	steps, err := Build(cfg, cfg, hetznerEnv())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	for _, s := range steps {
		if isDelete(s.Kind) {
			t.Errorf("identical configs should produce no deletes, got %s %s", s.Kind, s.Name)
		}
	}
}

func TestPlan_ServiceRemoved(t *testing.T) {
	prev := hetznerConfig()
	current := hetznerConfig()
	delete(current.Services, "meilisearch")

	steps, _ := Build(prev, current, hetznerEnv())
	if findStep(steps, StepServiceDelete, "meilisearch") == nil {
		t.Error("expected service.delete meilisearch")
	}
}

func TestPlan_VolumeRemoved(t *testing.T) {
	prev := hetznerConfig()
	current := hetznerConfig()
	delete(current.Volumes, "meili-data")

	steps, _ := Build(prev, current, hetznerEnv())
	if findStep(steps, StepVolumeDelete, "meili-data") == nil {
		t.Error("expected volume.delete meili-data")
	}
}

func TestPlan_StorageRemoved(t *testing.T) {
	prev := hetznerConfig()
	current := hetznerConfig()
	delete(current.Storage, "assets")

	steps, _ := Build(prev, current, hetznerEnv())
	if findStep(steps, StepStorageDelete, "assets") == nil {
		t.Error("expected storage.delete assets")
	}
}

func TestPlan_DNSRemoved(t *testing.T) {
	prev := hetznerConfig()
	current := hetznerConfig()
	delete(current.Domains, "web")

	steps, _ := Build(prev, current, hetznerEnv())
	s := findStep(steps, StepDNSDelete, "web")
	if s == nil {
		t.Error("expected dns.delete web")
	}
	domains := toStringSlice(s.Params["domains"])
	assertContains(t, domains, "final.nvoi.to")

	ingress := findStep(steps, StepIngressDelete, "web")
	if ingress == nil {
		t.Fatal("expected ingress.delete before dns.delete for domain removal")
	}
}

func TestPlan_DNSPartialRemoval(t *testing.T) {
	prev := hetznerConfig()
	prev.Domains["web"] = config.Domains{"final.nvoi.to", "www.nvoi.to"}
	current := hetznerConfig()
	current.Domains["web"] = config.Domains{"final.nvoi.to"}

	steps, _ := Build(prev, current, hetznerEnv())
	s := findStep(steps, StepDNSDelete, "web")
	if s == nil {
		t.Fatal("expected dns.delete web for removed domain")
	}

	domains := toStringSlice(s.Params["domains"])
	if len(domains) != 1 || domains[0] != "www.nvoi.to" {
		t.Fatalf("dns.delete domains = %v, want [www.nvoi.to]", domains)
	}

	ingressIdx := -1
	dnsIdx := -1
	for i, step := range steps {
		if step.Kind == StepIngressSet && ingressIdx == -1 {
			ingressIdx = i
		}
		if step.Kind == StepDNSDelete && step.Name == "web" {
			dnsIdx = i
		}
	}
	if ingressIdx == -1 || dnsIdx == -1 {
		t.Fatalf("expected both ingress.apply and dns.delete, got ingress=%d dns=%d", ingressIdx, dnsIdx)
	}
	if ingressIdx >= dnsIdx {
		t.Fatalf("ingress.apply at %d should come before dns.delete at %d", ingressIdx, dnsIdx)
	}
}

func TestPlan_DNSPartialAdditionDoesNotEmitDelete(t *testing.T) {
	prev := hetznerConfig()
	prev.Domains["web"] = config.Domains{"final.nvoi.to"}
	current := hetznerConfig()
	current.Domains["web"] = config.Domains{"final.nvoi.to", "www.nvoi.to"}

	steps, _ := Build(prev, current, hetznerEnv())

	if s := findStep(steps, StepDNSDelete, "web"); s != nil {
		t.Fatalf("did not expect dns.delete for domain addition, got %+v", *s)
	}

	ingress := findStep(steps, StepIngressSet, "web")
	if ingress == nil {
		t.Fatal("expected ingress.apply for domain addition")
	}

	dnsSet := findStep(steps, StepDNSSet, "web")
	if dnsSet == nil {
		t.Fatal("expected dns.set for expanded domain list")
	}
	domains := toStringSlice(dnsSet.Params["domains"])
	assertContains(t, domains, "final.nvoi.to")
	assertContains(t, domains, "www.nvoi.to")
}

func TestPlan_SecretRemoved(t *testing.T) {
	prev := hetznerConfig()
	current := hetznerConfig()
	web := current.Services["web"]
	web.Secrets = []string{"POSTGRES_PASSWORD"}
	current.Services["web"] = web
	jobs := current.Services["jobs"]
	jobs.Secrets = []string{"POSTGRES_PASSWORD"}
	current.Services["jobs"] = jobs

	steps, _ := Build(prev, current, hetznerEnv())
	if findStep(steps, StepSecretDelete, "RAILS_MASTER_KEY") == nil {
		t.Error("expected secret.delete RAILS_MASTER_KEY")
	}
	if findStep(steps, StepSecretDelete, "POSTGRES_PASSWORD") != nil {
		t.Error("POSTGRES_PASSWORD should not be deleted")
	}
}

func TestPlan_ComputeRemoved(t *testing.T) {
	prev := hetznerConfig()
	current := hetznerConfig()
	delete(current.Servers, "worker-1")

	steps, _ := Build(prev, current, hetznerEnv())
	if findStep(steps, StepComputeDelete, "worker-1") == nil {
		t.Error("expected instance.delete worker-1")
	}
	if findStep(steps, StepComputeDelete, "master") != nil {
		t.Error("master should not be deleted")
	}
}

func TestPlan_DeletesBeforeSets(t *testing.T) {
	prev := hetznerConfig()
	current := hetznerConfig()
	delete(current.Services, "meilisearch")
	delete(current.Volumes, "meili-data")

	steps, err := Build(prev, current, hetznerEnv())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	deleteIdx := -1
	firstSetIdx := -1
	for i, s := range steps {
		if s.Kind == StepServiceDelete && s.Name == "meilisearch" {
			deleteIdx = i
		}
		if s.Kind == StepComputeSet && firstSetIdx == -1 {
			firstSetIdx = i
		}
	}

	if deleteIdx == -1 {
		t.Fatal("expected service.delete meilisearch in plan")
	}
	if firstSetIdx == -1 {
		t.Fatal("expected instance.set in plan")
	}
	if deleteIdx >= firstSetIdx {
		t.Errorf("delete at %d should come before first set at %d", deleteIdx, firstSetIdx)
	}
}

func TestPlan_FirstDeploy_NoDeletes(t *testing.T) {
	steps, err := Build(nil, hetznerConfig(), hetznerEnv())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	for _, s := range steps {
		if isDelete(s.Kind) {
			t.Errorf("first deploy should have no deletes, got %s %s", s.Kind, s.Name)
		}
	}
}

func TestPlan_EmptyConfigDeletesAll(t *testing.T) {
	prev := hetznerConfig()
	empty := &Cfg{
		Servers:  map[string]config.Server{},
		Services: map[string]config.Service{},
	}

	steps, err := Build(prev, empty, nil)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	if len(steps) == 0 {
		t.Fatal("empty config against full prev should produce delete steps")
	}

	// Empty desired config should produce only deletes.
	for _, s := range steps {
		if !isDelete(s.Kind) && s.Kind != StepIngressDelete {
			t.Errorf("empty config should only produce deletes, got %s %s", s.Kind, s.Name)
		}
	}

	// Count by kind.
	kinds := map[StepKind]int{}
	for _, s := range steps {
		kinds[s.Kind]++
	}

	if kinds[StepDNSDelete] != 1 {
		t.Errorf("dns deletes = %d, want 1", kinds[StepDNSDelete])
	}
	if kinds[StepServiceDelete] != 4 {
		t.Errorf("service deletes = %d, want 4", kinds[StepServiceDelete])
	}
	if kinds[StepStorageDelete] != 1 {
		t.Errorf("storage deletes = %d, want 1", kinds[StepStorageDelete])
	}
	if kinds[StepSecretDelete] != 2 {
		t.Errorf("secret deletes = %d, want 2", kinds[StepSecretDelete])
	}
	if kinds[StepVolumeDelete] != 2 {
		t.Errorf("volume deletes = %d, want 2", kinds[StepVolumeDelete])
	}
	if kinds[StepComputeDelete] != 2 {
		t.Errorf("compute deletes = %d, want 2", kinds[StepComputeDelete])
	}

	// Verify guarded order: ingress delete first, dns delete second, compute last.
	if steps[0].Kind != StepIngressDelete {
		t.Errorf("first step = %s, want ingress.delete", steps[0].Kind)
	}
	if steps[1].Kind != StepDNSDelete {
		t.Errorf("second step = %s, want dns.delete", steps[1].Kind)
	}
	last := steps[len(steps)-1]
	if last.Kind != StepComputeDelete {
		t.Errorf("last step = %s, want instance.delete", last.Kind)
	}
}

func TestPlan_DomainRemovalOrdersIngressBeforeDNS(t *testing.T) {
	prev := hetznerConfig()
	current := hetznerConfig()
	delete(current.Domains, "web")

	steps, err := Build(prev, current, hetznerEnv())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	ingressIdx := -1
	dnsIdx := -1
	for i, s := range steps {
		if s.Kind == StepIngressDelete && ingressIdx == -1 {
			ingressIdx = i
		}
		if s.Kind == StepDNSDelete && s.Name == "web" {
			dnsIdx = i
		}
	}
	if ingressIdx == -1 {
		t.Fatal("expected ingress.delete step")
	}
	if dnsIdx == -1 {
		t.Fatal("expected dns.delete step")
	}
	if ingressIdx >= dnsIdx {
		t.Fatalf("ingress.delete at %d should come before dns.delete at %d", ingressIdx, dnsIdx)
	}
}

func isDelete(kind StepKind) bool {
	switch kind {
	case StepComputeDelete, StepVolumeDelete, StepSecretDelete,
		StepStorageDelete, StepServiceDelete, StepDNSDelete:
		return true
	}
	return false
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

// ── Firewall tests ────────────────────────────────────────────────────────────

func TestPlan_FirewallStep(t *testing.T) {
	cfg := hetznerConfig()
	cfg.Firewall = &config.FirewallConfig{Preset: "default"}

	steps, err := Build(nil, cfg, hetznerEnv())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	fw := findStep(steps, StepFirewallSet, "firewall")
	if fw == nil {
		t.Fatal("expected firewall.set step")
	}
	if fw.Params["preset"] != "default" {
		t.Errorf("firewall preset = %v, want default", fw.Params["preset"])
	}
}

func TestPlan_FirewallAfterCompute(t *testing.T) {
	cfg := hetznerConfig()
	cfg.Firewall = &config.FirewallConfig{Preset: "cloudflare"}

	steps, err := Build(nil, cfg, hetznerEnv())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	lastComputeIdx := -1
	firewallIdx := -1
	firstVolumeIdx := -1
	for i, s := range steps {
		if s.Kind == StepComputeSet {
			lastComputeIdx = i
		}
		if s.Kind == StepFirewallSet && firewallIdx == -1 {
			firewallIdx = i
		}
		if s.Kind == StepVolumeSet && firstVolumeIdx == -1 {
			firstVolumeIdx = i
		}
	}

	if firewallIdx == -1 {
		t.Fatal("firewall step not found")
	}
	if firewallIdx <= lastComputeIdx {
		t.Errorf("firewall at %d should be after last compute at %d", firewallIdx, lastComputeIdx)
	}
	if firstVolumeIdx != -1 && firewallIdx >= firstVolumeIdx {
		t.Errorf("firewall at %d should be before first volume at %d", firewallIdx, firstVolumeIdx)
	}
}

func TestPlan_NoFirewallWithoutConfig(t *testing.T) {
	steps, err := Build(nil, hetznerConfig(), hetznerEnv())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	for _, s := range steps {
		if s.Kind == StepFirewallSet {
			t.Error("expected no firewall.set step when no firewall config")
		}
	}
}

func TestPlan_FirewallWithRules(t *testing.T) {
	cfg := hetznerConfig()
	cfg.Firewall = &config.FirewallConfig{
		Preset: "cloudflare",
		Rules:  map[string][]string{"443": {"0.0.0.0/0"}},
	}

	steps, err := Build(nil, cfg, hetznerEnv())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	fw := findStep(steps, StepFirewallSet, "firewall")
	if fw == nil {
		t.Fatal("expected firewall.set step")
	}
	if fw.Params["preset"] != "cloudflare" {
		t.Errorf("preset = %v", fw.Params["preset"])
	}
	rulesRaw, ok := fw.Params["rules"]
	if !ok {
		t.Fatal("expected rules in params")
	}
	rules, ok := rulesRaw.(map[string][]string)
	if !ok {
		t.Fatalf("rules type = %T, want map[string][]string", rulesRaw)
	}
	if rules["443"][0] != "0.0.0.0/0" {
		t.Errorf("443 rule = %v", rules["443"])
	}
}
