package cloud

import (
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
)

func TestServiceSet(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Services["web"] = config.ServiceDef{Build: "web", Port: 3000}
	// Build target "web" not defined — validation catches it.
	mustFailValidation(t, cfg, "not a defined build target")
}

func TestServiceSet_Valid(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Services["web"] = config.ServiceDef{Image: "nginx", Port: 80}
	mustValidate(t, cfg)
}

func TestServiceSet_Upsert(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Services["web"] = config.ServiceDef{Image: "nginx", Port: 3000}
	cfg.Services["web"] = config.ServiceDef{Image: "nginx", Port: 8080}
	if cfg.Services["web"].Port != 8080 {
		t.Fatalf("port = %d, want 8080 after upsert", cfg.Services["web"].Port)
	}
	mustValidate(t, cfg)
}

func TestServiceSet_MissingImageAndBuild(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Services["web"] = config.ServiceDef{Port: 3000}
	mustFailValidation(t, cfg, "image or build is required")
}

func TestServiceSet_ImageAndBuildMutuallyExclusive(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Services["web"] = config.ServiceDef{Image: "nginx", Build: "web", Port: 80}
	mustFailValidation(t, cfg, "mutually exclusive")
}

func TestServiceSet_InvalidServerRef(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Services["web"] = config.ServiceDef{Image: "nginx", Port: 80, Server: "nonexistent"}
	mustFailValidation(t, cfg, "not a defined server")
}

func TestServiceSet_InvalidStorageRef(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Services["web"] = config.ServiceDef{Image: "nginx", Port: 80, Storage: []string{"nonexistent"}}
	mustFailValidation(t, cfg, "not a defined storage")
}

func TestServiceRemove(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Services["web"] = config.ServiceDef{Image: "nginx", Port: 80}
	cfg.Domains = map[string][]string{"web": {"example.com"}}

	delete(cfg.Services, "web")
	delete(cfg.Domains, "web")

	if _, ok := cfg.Services["web"]; ok {
		t.Fatal("service should be removed")
	}
	if _, ok := cfg.Domains["web"]; ok {
		t.Fatal("domains should be removed with service")
	}
	mustValidate(t, cfg)
}
