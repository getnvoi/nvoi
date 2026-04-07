package config

import (
	"strings"
	"testing"
)

func validConfig() *Config {
	return &Config{
		Servers: map[string]Server{
			"master": {Type: "cx23", Region: "fsn1"},
		},
		Services: map[string]Service{
			"web": {Image: "nginx", Port: 80},
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
	cfg.Servers["master"] = Server{Type: "cx23"}
	errs := Validate(cfg)
	assertHasError(t, errs, "servers.master.region")
}

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

func TestValidate_ServiceNoSource(t *testing.T) {
	cfg := validConfig()
	cfg.Services["web"] = Service{Port: 80}
	errs := Validate(cfg)
	assertHasError(t, errs, "must have one of image, build, or managed")
}

func TestValidate_ServiceMultipleSources(t *testing.T) {
	cfg := validConfig()
	cfg.Build = map[string]Build{"web": {Source: "org/repo"}}
	cfg.Services["web"] = Service{Image: "nginx", Build: "web", Port: 80}
	errs := Validate(cfg)
	assertHasError(t, errs, "mutually exclusive")
}

func TestValidate_ServiceManagedValid(t *testing.T) {
	cfg := validConfig()
	cfg.Services["db"] = Service{Managed: "postgres"}
	errs := Validate(cfg)
	if len(errs) > 0 {
		t.Errorf("managed postgres should be valid, got: %v", errs)
	}
}

func TestValidate_ServiceManagedPlusImage(t *testing.T) {
	cfg := validConfig()
	cfg.Services["db"] = Service{Managed: "postgres", Image: "postgres:17"}
	errs := Validate(cfg)
	assertHasError(t, errs, "mutually exclusive")
}

func TestValidate_ServiceUsesRefValid(t *testing.T) {
	cfg := validConfig()
	cfg.Services["db"] = Service{Managed: "postgres"}
	cfg.Services["web"] = Service{Image: "nginx", Port: 80, Uses: []string{"db"}}
	errs := Validate(cfg)
	if len(errs) > 0 {
		t.Errorf("uses ref to managed service should be valid, got: %v", errs)
	}
}

func TestValidate_ServiceUsesRefInvalid(t *testing.T) {
	cfg := validConfig()
	cfg.Services["web"] = Service{Image: "nginx", Uses: []string{"nonexistent"}}
	errs := Validate(cfg)
	assertHasError(t, errs, "not a managed service")
}

func TestValidate_ServiceBuildRefMissing(t *testing.T) {
	cfg := validConfig()
	cfg.Services["web"] = Service{Build: "nonexistent", Port: 80}
	errs := Validate(cfg)
	assertHasError(t, errs, "not a defined build target")
}

func TestValidate_ServiceServerRefMissing(t *testing.T) {
	cfg := validConfig()
	cfg.Services["web"] = Service{Image: "nginx", Server: "nonexistent"}
	errs := Validate(cfg)
	assertHasError(t, errs, "not a defined server")
}

func TestValidate_ServiceStorageRefMissing(t *testing.T) {
	cfg := validConfig()
	cfg.Services["web"] = Service{Image: "nginx", Storage: []string{"nonexistent"}}
	errs := Validate(cfg)
	assertHasError(t, errs, "not a defined storage")
}

func TestValidate_ServiceVolumeRefMissing(t *testing.T) {
	cfg := validConfig()
	cfg.Services["web"] = Service{Image: "nginx", Volumes: []string{"pgdata:/data"}}
	errs := Validate(cfg)
	assertHasError(t, errs, "volume \"pgdata\" is not defined")
}

func TestValidate_ServiceVolumeBadFormat(t *testing.T) {
	cfg := validConfig()
	cfg.Services["web"] = Service{Image: "nginx", Volumes: []string{"nopath"}}
	errs := Validate(cfg)
	assertHasError(t, errs, "must be name:/path")
}

func TestValidate_ServiceVolumeAbsolutePathOK(t *testing.T) {
	cfg := validConfig()
	cfg.Services["web"] = Service{Image: "nginx", Volumes: []string{"/host/path:/container/path"}}
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
	cfg.Services["web"] = Service{Image: "nginx"} // no port
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
	cfg.Services["db"] = Service{Image: "postgres:17", Volumes: []string{"pgdata:/var/lib/postgresql/data"}}
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
			"master":   {Type: "cx23", Region: "fsn1"},
			"worker-1": {Type: "cx33", Region: "fsn1"},
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
			"db":          {Image: "postgres:17", Volumes: []string{"pgdata:/var/lib/postgresql/data"}, Secrets: []string{"POSTGRES_PASSWORD"}},
			"meilisearch": {Image: "getmeili/meilisearch:latest", Volumes: []string{"meili-data:/meili_data"}},
			"web":         {Build: "web", Port: 80, Replicas: 2, Health: "/up", Server: "worker-1", Storage: []string{"assets"}},
			"jobs":        {Build: "web", Command: "bin/jobs", Server: "worker-1"},
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

func TestValidate_ProxyWithDefaultFirewall(t *testing.T) {
	cfg := validConfig()
	cfg.Firewall = &FirewallConfig{Preset: "default"}
	cfg.Domains = map[string]Domains{"web": {"example.com"}}
	cfg.Ingress = &IngressConfig{Exposure: "edge_proxied"}
	errs := Validate(cfg)
	assertHasError(t, errs, "proxied edge mode currently requires \"firewall: cloudflare\"")
}

func TestValidate_CloudflareFirewallWithoutProxy(t *testing.T) {
	cfg := validConfig()
	cfg.Firewall = &FirewallConfig{Preset: "cloudflare"}
	cfg.Domains = map[string]Domains{"web": {"example.com"}}
	errs := Validate(cfg)
	assertHasError(t, errs, "firewall: preset \"cloudflare\" requires ingress.exposure: edge_proxied")
}

func TestValidate_CloudflareFirewallWithProxy(t *testing.T) {
	cfg := validConfig()
	cfg.Firewall = &FirewallConfig{Preset: "cloudflare"}
	cfg.Domains = map[string]Domains{"web": {"example.com"}}
	cfg.Ingress = &IngressConfig{
		Exposure: "edge_proxied",
		Edge:     &IngressEdgeConfig{Provider: "cloudflare"},
	}
	errs := Validate(cfg)
	assertNoError(t, errs, "firewall")
	assertNoError(t, errs, "cloudflare")
}

func TestValidate_IngressConfigProvidedTLSValid(t *testing.T) {
	cfg := validConfig()
	cfg.Firewall = &FirewallConfig{Preset: "default"}
	cfg.Domains = map[string]Domains{"web": {"example.com"}}
	cfg.Ingress = &IngressConfig{
		Exposure: "direct",
		TLS: &IngressTLSConfig{
			Mode: "provided",
			Cert: "TLS_CERT_PEM",
			Key:  "TLS_KEY_PEM",
		},
	}

	errs := Validate(cfg)
	assertNoError(t, errs, "ingress")
}

func TestValidate_IngressConfigProvidedTLSRequiresBothRefs(t *testing.T) {
	cfg := validConfig()
	cfg.Firewall = &FirewallConfig{Preset: "default"}
	cfg.Domains = map[string]Domains{"web": {"example.com"}}
	cfg.Ingress = &IngressConfig{
		Exposure: "direct",
		TLS: &IngressTLSConfig{
			Mode: "provided",
			Cert: "TLS_CERT_PEM",
		},
	}

	errs := Validate(cfg)
	assertHasError(t, errs, "cert and key must both be set")
}

func TestValidate_EdgeOriginRequiresExplicitCloudflareOverlay(t *testing.T) {
	cfg := validConfig()
	cfg.Firewall = &FirewallConfig{Preset: "cloudflare"}
	cfg.Domains = map[string]Domains{"web": {"example.com"}}
	cfg.Ingress = &IngressConfig{
		Exposure: "edge_proxied",
		TLS: &IngressTLSConfig{
			Mode: "edge_origin",
		},
	}

	errs := Validate(cfg)
	assertHasError(t, errs, "requires ingress.edge.provider")
}

func TestValidate_EdgeOverlayRequiresExplicitExposure(t *testing.T) {
	cfg := validConfig()
	cfg.Firewall = &FirewallConfig{Preset: "default"}
	cfg.Domains = map[string]Domains{"web": {"example.com"}}
	cfg.Ingress = &IngressConfig{
		Edge: &IngressEdgeConfig{Provider: "cloudflare"},
	}

	errs := Validate(cfg)
	assertHasError(t, errs, "edge overlays require ingress.exposure")
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
