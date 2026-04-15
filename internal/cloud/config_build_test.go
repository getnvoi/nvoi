package cloud

import (
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
)

func TestBuildSet_Valid(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Build = map[string]string{"api": "./cmd/api"}
	mustValidate(t, cfg)
}

func TestBuildSet_EmptySource(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Build = map[string]string{"api": ""}
	mustFailValidation(t, cfg, "source is required")
}

func TestBuildSet_Upsert(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Build = map[string]string{"api": "./cmd/api"}
	cfg.Build["api"] = "./cmd/api-v2"
	if cfg.Build["api"] != "./cmd/api-v2" {
		t.Fatalf("path = %q, want ./cmd/api-v2", cfg.Build["api"])
	}
	mustValidate(t, cfg)
}

func TestBuildRemove(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Build = map[string]string{"api": "./cmd/api"}
	delete(cfg.Build, "api")
	mustValidate(t, cfg)
}

func TestBuildRemove_ReferencedByService(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Build = map[string]string{"api": "./cmd/api"}
	cfg.Services["api"] = config.ServiceDef{Build: "api", Port: 8080}
	mustValidate(t, cfg)

	// Remove build — service still references it.
	delete(cfg.Build, "api")
	mustFailValidation(t, cfg, "not a defined build target")
}
