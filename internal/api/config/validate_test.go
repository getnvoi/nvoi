package config

import (
	"strings"
	"testing"
)

func validConfig() *Config {
	return &Config{
		Servers: map[string]Server{
			"master": {Type: "cx23", Region: "fsn1", Role: "master"},
		},
		Services: map[string]Service{
			"web": {Workload: Workload{Image: "nginx"}, Port: 80},
		},
	}
}

func TestValidate_ValidMinimal(t *testing.T) {
	errs := Validate(validConfig())
	if len(errs) > 0 {
		t.Errorf("expected no errors, got: %v", errs)
	}
}

func TestValidate_NoServers(t *testing.T) {
	cfg := validConfig()
	cfg.Servers = nil
	cfg.Services = nil
	errs := Validate(cfg)
	// Empty config is valid — used for destroy-via-diff.
	if len(errs) != 0 {
		t.Errorf("empty config should be valid, got: %v", errs)
	}
}

func TestValidate_ServerMissingType(t *testing.T) {
	cfg := validConfig()
	cfg.Servers["master"] = Server{Region: "fsn1"}
	errs := Validate(cfg)
	assertHasError(t, errs, "servers.master.type")
}

func TestValidate_ServerMissingRegion(t *testing.T) {
	cfg := validConfig()
	cfg.Servers["master"] = Server{Type: "cx23", Role: "master"}
	errs := Validate(cfg)
	assertHasError(t, errs, "servers.master.region")
}

func TestValidate_ServerMissingRole(t *testing.T) {
	cfg := validConfig()
	cfg.Servers["master"] = Server{Type: "cx23", Region: "fsn1"} // no role
	errs := Validate(cfg)
	assertHasError(t, errs, "role: required")
}

func TestValidate_ServerInvalidRole(t *testing.T) {
	cfg := validConfig()
	cfg.Servers["master"] = Server{Type: "cx23", Region: "fsn1", Role: "leader"}
	errs := Validate(cfg)
	assertHasError(t, errs, "must be master or worker")
}

func TestValidate_MultipleServers_NoMaster(t *testing.T) {
	cfg := &Config{
		Servers: map[string]Server{
			"node-1": {Type: "cx23", Region: "fsn1", Role: "worker"},
			"node-2": {Type: "cx23", Region: "fsn1", Role: "worker"},
		},
		Services: map[string]Service{
			"web": {Workload: Workload{Image: "nginx"}, Port: 80},
		},
	}
	errs := Validate(cfg)
	assertHasError(t, errs, "exactly one server must have role: master")
}

func TestValidate_MultipleServers_TwoMasters(t *testing.T) {
	cfg := &Config{
		Servers: map[string]Server{
			"master-1": {Type: "cx23", Region: "fsn1", Role: "master"},
			"master-2": {Type: "cx23", Region: "fsn1", Role: "master"},
		},
		Services: map[string]Service{
			"web": {Workload: Workload{Image: "nginx"}, Port: 80},
		},
	}
	errs := Validate(cfg)
	assertHasError(t, errs, "multiple masters not yet supported")
}

// ── Cron validation ───────────────────────────────────────────────────────────

func TestValidate_CronMissingImage(t *testing.T) {
	cfg := validConfig()
	cfg.Crons = map[string]Cron{"backup": {Schedule: "0 2 * * *"}}
	errs := Validate(cfg)
	assertHasError(t, errs, "crons.backup.image: required")
}

func TestValidate_CronMissingSchedule(t *testing.T) {
	cfg := validConfig()
	cfg.Crons = map[string]Cron{"backup": {Workload: Workload{Image: "postgres:17"}}}
	errs := Validate(cfg)
	assertHasError(t, errs, "crons.backup.schedule: required")
}

func TestValidate_CronServerNotDefined(t *testing.T) {
	cfg := validConfig()
	cfg.Crons = map[string]Cron{"backup": {Workload: Workload{Image: "pg:17", Server: "nonexistent"}, Schedule: "0 2 * * *"}}
	errs := Validate(cfg)
	assertHasError(t, errs, "crons.backup.server")
}

func TestValidate_CronStorageNotDefined(t *testing.T) {
	cfg := validConfig()
	cfg.Crons = map[string]Cron{"backup": {Workload: Workload{Image: "pg:17", Storage: []string{"missing"}}, Schedule: "0 2 * * *"}}
	errs := Validate(cfg)
	assertHasError(t, errs, "crons.backup.storage")
}

func TestValidate_CronVolumeNotDefined(t *testing.T) {
	cfg := validConfig()
	cfg.Crons = map[string]Cron{"backup": {Workload: Workload{Image: "pg:17", Volumes: []string{"missing:/data"}}, Schedule: "0 2 * * *"}}
	errs := Validate(cfg)
	assertHasError(t, errs, "crons.backup.volumes")
}

func TestValidate_CronVolumeBadFormat(t *testing.T) {
	cfg := validConfig()
	cfg.Crons = map[string]Cron{"backup": {Workload: Workload{Image: "pg:17", Volumes: []string{"nocolon"}}, Schedule: "0 2 * * *"}}
	errs := Validate(cfg)
	assertHasError(t, errs, "must be name:/path")
}

func TestValidate_CronValid(t *testing.T) {
	cfg := validConfig()
	cfg.Storage = map[string]Storage{"backups": {}}
	cfg.Volumes = map[string]Volume{"data": {Size: 10, Server: "master"}}
	cfg.Crons = map[string]Cron{"backup": {
		Workload: Workload{Image: "pg:17", Server: "master", Storage: []string{"backups"}, Volumes: []string{"data:/data"}},
		Schedule: "0 2 * * *",
	}}
	errs := Validate(cfg)
	assertNoError(t, errs, "crons")
}

// ── Services ──────────────────────────────────────────────────────────────────

func TestValidate_NoServices(t *testing.T) {
	cfg := validConfig()
	cfg.Services = nil
	cfg.Servers = nil
	errs := Validate(cfg)
	// Empty config is valid — used for destroy-via-diff.
	if len(errs) != 0 {
		t.Errorf("empty config should be valid, got: %v", errs)
	}
}

func TestValidate_ServiceNoImage(t *testing.T) {
	cfg := validConfig()
	cfg.Services["web"] = Service{Port: 80}
	errs := Validate(cfg)
	assertHasError(t, errs, "image is required")
}

func TestValidate_ServiceManagedValid(t *testing.T) {
	cfg := validConfig()
	cfg.Services["db"] = Service{Managed: "postgres"}
	errs := Validate(cfg)
	if len(errs) > 0 {
		t.Errorf("managed postgres should be valid, got: %v", errs)
	}
}

func TestValidate_ServiceManagedPlusImage_Allowed(t *testing.T) {
	// Image on a managed service is the custom base image — not a separate source.
	cfg := validConfig()
	cfg.Services["db"] = Service{Workload: Workload{Image: "postgres:16"}, Managed: "postgres"}
	errs := Validate(cfg)
	assertNoError(t, errs, "mutually exclusive")
}

func TestValidate_ServiceUsesRefValid(t *testing.T) {
	cfg := validConfig()
	cfg.Services["db"] = Service{Managed: "postgres"}
	cfg.Services["web"] = Service{Workload: Workload{Image: "nginx"}, Port: 80, Uses: []string{"db"}}
	errs := Validate(cfg)
	if len(errs) > 0 {
		t.Errorf("uses ref to managed service should be valid, got: %v", errs)
	}
}

func TestValidate_ManagedRejectsIncompatibleFields(t *testing.T) {
	cfg := validConfig()
	cfg.Services["db"] = Service{
		Workload: Workload{Command: "postgres", Env: []string{"FOO=bar"}},
		Managed:  "postgres",
		Port:     5432,
		Replicas: 2,
		Health:   "/healthz",
		Uses:     []string{"other"},
	}
	errs := Validate(cfg)
	assertHasError(t, errs, "port not supported on managed")
	assertHasError(t, errs, "replicas not supported on managed")
	assertHasError(t, errs, "command not supported on managed")
	assertHasError(t, errs, "health not supported on managed")
	assertHasError(t, errs, "env not supported on managed")
	assertHasError(t, errs, "uses not supported on managed")
}

func TestValidate_ManagedAllowsSecretsAndImage(t *testing.T) {
	cfg := validConfig()
	cfg.Services["db"] = Service{
		Workload: Workload{Image: "postgres:16", Secrets: []string{"POSTGRES_PASSWORD"}},
		Managed:  "postgres",
	}
	errs := Validate(cfg)
	assertNoError(t, errs, "not supported on managed")
}

func TestValidate_ServiceUsesRefInvalid(t *testing.T) {
	cfg := validConfig()
	cfg.Services["web"] = Service{Workload: Workload{Image: "nginx"}, Uses: []string{"nonexistent"}}
	errs := Validate(cfg)
	assertHasError(t, errs, "not a managed service")
}

func TestValidate_ServiceServerRefMissing(t *testing.T) {
	cfg := validConfig()
	cfg.Services["web"] = Service{Workload: Workload{Image: "nginx", Server: "nonexistent"}}
	errs := Validate(cfg)
	assertHasError(t, errs, "not a defined server")
}

func TestValidate_ServiceStorageRefMissing(t *testing.T) {
	cfg := validConfig()
	cfg.Services["web"] = Service{Workload: Workload{Image: "nginx", Storage: []string{"nonexistent"}}}
	errs := Validate(cfg)
	assertHasError(t, errs, "not a defined storage")
}

func TestValidate_ServiceVolumeRefMissing(t *testing.T) {
	cfg := validConfig()
	cfg.Services["web"] = Service{Workload: Workload{Image: "nginx", Volumes: []string{"pgdata:/data"}}}
	errs := Validate(cfg)
	assertHasError(t, errs, "volume \"pgdata\" is not defined")
}

func TestValidate_ServiceVolumeBadFormat(t *testing.T) {
	cfg := validConfig()
	cfg.Services["web"] = Service{Workload: Workload{Image: "nginx", Volumes: []string{"nopath"}}}
	errs := Validate(cfg)
	assertHasError(t, errs, "must be name:/path")
}

func TestValidate_ServiceVolumeAbsolutePathOK(t *testing.T) {
	cfg := validConfig()
	cfg.Services["web"] = Service{Workload: Workload{Image: "nginx", Volumes: []string{"/host/path:/container/path"}}}
	errs := Validate(cfg)
	if len(errs) > 0 {
		t.Errorf("absolute path volume should be valid, got: %v", errs)
	}
}

func TestValidate_VolumeServerRefMissing(t *testing.T) {
	cfg := validConfig()
	cfg.Volumes = map[string]Volume{"pgdata": {Size: 30, Server: "nonexistent"}}
	errs := Validate(cfg)
	assertHasError(t, errs, "not a defined server")
}

func TestValidate_VolumeSizeZero(t *testing.T) {
	cfg := validConfig()
	cfg.Volumes = map[string]Volume{"pgdata": {Size: 0, Server: "master"}}
	errs := Validate(cfg)
	assertHasError(t, errs, "must be > 0")
}

func TestValidate_BuildMissingSource(t *testing.T) {
	cfg := validConfig()
	cfg.Build = map[string]Build{"web": {Source: ""}}
	errs := Validate(cfg)
	assertHasError(t, errs, "build.web.source")
}

func TestValidate_DomainServiceMissing(t *testing.T) {
	cfg := validConfig()
	cfg.Firewall = &FirewallConfig{Preset: "default"}
	cfg.Domains = map[string]Domains{"api": {"api.example.com"}}
	errs := Validate(cfg)
	assertHasError(t, errs, "not a defined service")
}

func TestValidate_DomainServiceNoPort(t *testing.T) {
	cfg := validConfig()
	cfg.Firewall = &FirewallConfig{Preset: "default"}
	cfg.Services["web"] = Service{Workload: Workload{Image: "nginx"}} // no port
	cfg.Domains = map[string]Domains{"web": {"example.com"}}
	errs := Validate(cfg)
	assertHasError(t, errs, "has no port")
}

func TestValidate_DomainEmpty(t *testing.T) {
	cfg := validConfig()
	cfg.Firewall = &FirewallConfig{Preset: "default"}
	cfg.Domains = map[string]Domains{"web": {}}
	errs := Validate(cfg)
	assertHasError(t, errs, "at least one domain")
}

func TestValidate_OrphanVolume(t *testing.T) {
	cfg := validConfig()
	cfg.Volumes = map[string]Volume{"pgdata": {Size: 30, Server: "master"}}
	// No service mounts pgdata.
	errs := Validate(cfg)
	assertHasError(t, errs, "volumes.pgdata: defined but not mounted")
}

func TestValidate_VolumeUsedByService(t *testing.T) {
	cfg := validConfig()
	cfg.Volumes = map[string]Volume{"pgdata": {Size: 30, Server: "master"}}
	cfg.Services["db"] = Service{Workload: Workload{Image: "postgres:17", Volumes: []string{"pgdata:/var/lib/postgresql/data"}}}
	errs := Validate(cfg)
	for _, err := range errs {
		if strings.Contains(err.Error(), "not mounted") {
			t.Errorf("pgdata is mounted by db, should not be orphan: %v", err)
		}
	}
}

func TestValidate_FullConfig(t *testing.T) {
	cfg := &Config{
		Servers: map[string]Server{
			"master":   {Type: "cx23", Region: "fsn1", Role: "master"},
			"worker-1": {Type: "cx33", Region: "fsn1", Role: "worker"},
		},
		Firewall: &FirewallConfig{Preset: "default"},
		Volumes: map[string]Volume{
			"pgdata":     {Size: 30, Server: "master"},
			"meili-data": {Size: 20, Server: "master"},
		},
		Build: map[string]Build{
			"web": {Source: "benbonnet/dummy-rails"},
		},
		Storage: map[string]Storage{
			"assets": {CORS: true},
		},
		Services: map[string]Service{
			"db":          {Workload: Workload{Image: "postgres:17", Volumes: []string{"pgdata:/var/lib/postgresql/data"}, Secrets: []string{"POSTGRES_PASSWORD"}}},
			"meilisearch": {Workload: Workload{Image: "getmeili/meilisearch:latest", Volumes: []string{"meili-data:/meili_data"}}},
			"web":         {Workload: Workload{Image: "web", Server: "worker-1", Storage: []string{"assets"}}, Port: 80, Replicas: 2, Health: "/up"},
			"jobs":        {Workload: Workload{Image: "web", Command: "bin/jobs", Server: "worker-1"}},
		},
		Domains: map[string]Domains{
			"web": {"final.nvoi.to"},
		},
	}
	errs := Validate(cfg)
	if len(errs) > 0 {
		t.Errorf("expected no errors, got: %v", errs)
	}
}

func TestValidate_MultipleErrors(t *testing.T) {
	cfg := &Config{
		Servers: map[string]Server{
			"master": {Type: "", Region: ""}, // missing both
		},
		Services: map[string]Service{
			"web": {Port: 80}, // missing image/build/managed
		},
	}
	errs := Validate(cfg)
	if len(errs) < 2 {
		t.Errorf("expected multiple errors, got %d: %v", len(errs), errs)
	}
}

// ── Firewall × Domains × Proxy coherence ──────────────────────────────────────

func TestValidate_DomainsWithoutFirewall(t *testing.T) {
	cfg := validConfig()
	cfg.Domains = map[string]Domains{"web": {"example.com"}}
	// No Firewall set
	errs := Validate(cfg)
	assertHasError(t, errs, "no firewall section")
}

func TestValidate_DomainsWithoutPort80(t *testing.T) {
	cfg := validConfig()
	cfg.Firewall = &FirewallConfig{Rules: map[string][]string{"22": {"0.0.0.0/0"}}} // no 80/443
	cfg.Domains = map[string]Domains{"web": {"example.com"}}
	errs := Validate(cfg)
	assertHasError(t, errs, "ports 80/443 not open")
}

func TestValidate_DomainsWithFirewall(t *testing.T) {
	cfg := validConfig()
	cfg.Firewall = &FirewallConfig{Preset: "default"}
	cfg.Domains = map[string]Domains{"web": {"example.com"}}
	errs := Validate(cfg)
	assertNoError(t, errs, "firewall")
}

func TestValidate_NoDomainsNoFirewall(t *testing.T) {
	cfg := validConfig()
	// No domains, no firewall — valid (worker-only setup)
	errs := Validate(cfg)
	assertNoError(t, errs, "firewall")
}

func TestValidate_CloudflareFirewallWithoutDomains_Valid(t *testing.T) {
	// During incremental build-up: firewall set before domains/ingress.
	cfg := validConfig()
	cfg.Firewall = &FirewallConfig{Preset: "cloudflare"}
	errs := Validate(cfg)
	assertNoError(t, errs, "cloudflare")
	assertNoError(t, errs, "ingress")
}

func TestValidate_CloudflareManagedWithoutDomains_Valid(t *testing.T) {
	// Ingress config set before domains — no coherence error yet.
	cfg := validConfig()
	cfg.Firewall = &FirewallConfig{Preset: "cloudflare"}
	cfg.Ingress = &IngressConfig{CloudflareManaged: true}
	errs := Validate(cfg)
	assertNoError(t, errs, "firewall")
	assertNoError(t, errs, "cloudflare")
}

func TestValidate_CloudflareManagedWithDefaultFirewall(t *testing.T) {
	cfg := validConfig()
	cfg.Firewall = &FirewallConfig{Preset: "default"}
	cfg.Domains = map[string]Domains{"web": {"example.com"}}
	cfg.Ingress = &IngressConfig{CloudflareManaged: true}
	errs := Validate(cfg)
	assertHasError(t, errs, "cloudflare-managed requires \"firewall: cloudflare\"")
}

func TestValidate_CloudflareFirewallWithoutCFManaged(t *testing.T) {
	cfg := validConfig()
	cfg.Firewall = &FirewallConfig{Preset: "cloudflare"}
	cfg.Domains = map[string]Domains{"web": {"example.com"}}
	errs := Validate(cfg)
	assertHasError(t, errs, "preset \"cloudflare\" requires ingress.cloudflare-managed: true")
}

func TestValidate_CloudflareFirewallWithCFManaged(t *testing.T) {
	cfg := validConfig()
	cfg.Firewall = &FirewallConfig{Preset: "cloudflare"}
	cfg.Domains = map[string]Domains{"web": {"example.com"}}
	cfg.Ingress = &IngressConfig{CloudflareManaged: true}
	errs := Validate(cfg)
	assertNoError(t, errs, "firewall")
	assertNoError(t, errs, "cloudflare")
}

func TestValidate_CustomCertValid(t *testing.T) {
	cfg := validConfig()
	cfg.Firewall = &FirewallConfig{Preset: "default"}
	cfg.Domains = map[string]Domains{"web": {"example.com"}}
	cfg.Ingress = &IngressConfig{Cert: "TLS_CERT_PEM", Key: "TLS_KEY_PEM"}
	errs := Validate(cfg)
	assertNoError(t, errs, "ingress")
}

func TestValidate_CertWithoutKey(t *testing.T) {
	cfg := validConfig()
	cfg.Firewall = &FirewallConfig{Preset: "default"}
	cfg.Domains = map[string]Domains{"web": {"example.com"}}
	cfg.Ingress = &IngressConfig{Cert: "TLS_CERT_PEM"}
	errs := Validate(cfg)
	assertHasError(t, errs, "cert requires key")
}

func TestValidate_CloudflareManagedAndCertMutuallyExclusive(t *testing.T) {
	cfg := validConfig()
	cfg.Firewall = &FirewallConfig{Preset: "cloudflare"}
	cfg.Domains = map[string]Domains{"web": {"example.com"}}
	cfg.Ingress = &IngressConfig{CloudflareManaged: true, Cert: "x", Key: "y"}
	errs := Validate(cfg)
	assertHasError(t, errs, "cloudflare-managed and cert/key are mutually exclusive")
}

func assertNoError(t *testing.T, errs []error, substr string) {
	t.Helper()
	for _, err := range errs {
		if strings.Contains(err.Error(), substr) {
			t.Errorf("unexpected error containing %q: %v", substr, err)
		}
	}
}

func assertHasError(t *testing.T, errs []error, substr string) {
	t.Helper()
	for _, err := range errs {
		if strings.Contains(err.Error(), substr) {
			return
		}
	}
	t.Errorf("expected error containing %q, got: %v", substr, errs)
}
