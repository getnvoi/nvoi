package cloud

import (
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
)

func TestDomainAdd_Valid(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Services["web"] = config.ServiceDef{Image: "nginx", Port: 80}
	cfg.Domains = map[string][]string{"web": {"example.com"}}
	mustValidate(t, cfg)
}

func TestDomainAdd_ServiceNotDefined(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Domains = map[string][]string{"ghost": {"example.com"}}
	mustFailValidation(t, cfg, "not a defined service")
}

func TestDomainAdd_ServiceNoPort(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Services["web"] = config.ServiceDef{Image: "nginx"}
	cfg.Domains = map[string][]string{"web": {"example.com"}}
	mustFailValidation(t, cfg, "has no port")
}

func TestDomainAdd_Multiple(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Services["web"] = config.ServiceDef{Image: "nginx", Port: 80}
	cfg.Domains = map[string][]string{"web": {"example.com", "www.example.com"}}
	mustValidate(t, cfg)
}

func TestDomainAdd_Duplicate(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Services["web"] = config.ServiceDef{Image: "nginx", Port: 80}
	cfg.Domains = map[string][]string{"web": {"example.com"}}

	// Dedup.
	found := false
	for _, d := range cfg.Domains["web"] {
		if d == "example.com" {
			found = true
			break
		}
	}
	if !found {
		cfg.Domains["web"] = append(cfg.Domains["web"], "example.com")
	}
	if len(cfg.Domains["web"]) != 1 {
		t.Fatalf("domains = %v, want exactly one", cfg.Domains["web"])
	}
	mustValidate(t, cfg)
}

func TestDomainRemove(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Services["web"] = config.ServiceDef{Image: "nginx", Port: 80}
	cfg.Domains = map[string][]string{"web": {"example.com", "www.example.com"}}

	filtered := []string{}
	for _, d := range cfg.Domains["web"] {
		if d != "example.com" {
			filtered = append(filtered, d)
		}
	}
	cfg.Domains["web"] = filtered

	if len(cfg.Domains["web"]) != 1 {
		t.Fatalf("domains = %v, want 1", cfg.Domains["web"])
	}
	mustValidate(t, cfg)
}

func TestDomainRemove_Last(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Services["web"] = config.ServiceDef{Image: "nginx", Port: 80}
	cfg.Domains = map[string][]string{"web": {"example.com"}}

	filtered := []string{}
	for _, d := range cfg.Domains["web"] {
		if d != "example.com" {
			filtered = append(filtered, d)
		}
	}
	if len(filtered) == 0 {
		delete(cfg.Domains, "web")
	}

	if _, ok := cfg.Domains["web"]; ok {
		t.Fatal("key should be removed when no domains remain")
	}
	mustValidate(t, cfg)
}

func TestDomainAdd_ExplicitReplicas1_WebFacing(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Services["web"] = config.ServiceDef{Image: "nginx", Port: 80, Replicas: 1}
	cfg.Domains = map[string][]string{"web": {"example.com"}}
	mustFailValidation(t, cfg, "replicas >= 2")
}
