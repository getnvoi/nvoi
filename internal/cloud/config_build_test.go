package cloud

import (
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
)

func TestBuildSet_Valid(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Build = map[string]string{"web": "./cmd/web"}
	mustValidate(t, cfg)
}

func TestBuildSet_EmptySource(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Build = map[string]string{"web": ""}
	mustFailValidation(t, cfg, "source is required")
}

func TestBuildSet_Upsert(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Build = map[string]string{"web": "./cmd/web"}
	cfg.Build["web"] = "./cmd/web-v2"
	if cfg.Build["web"] != "./cmd/web-v2" {
		t.Fatalf("path = %q, want ./cmd/web-v2", cfg.Build["web"])
	}
	mustValidate(t, cfg)
}

func TestBuildRemove(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Build = map[string]string{"web": "./cmd/web"}
	delete(cfg.Build, "web")
	mustValidate(t, cfg)
}

func TestBuildRemove_ReferencedByService(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Build = map[string]string{"web": "./cmd/web"}
	cfg.Services["web"] = config.ServiceDef{Build: "web", Port: 3000}
	mustValidate(t, cfg)

	// Remove build — service still references it.
	delete(cfg.Build, "web")
	mustFailValidation(t, cfg, "not a defined build target")
}
