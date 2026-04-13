package cloud

import (
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/internal/reconcile"

	_ "github.com/getnvoi/nvoi/internal/packages/database"
)

// freshConfig returns a minimal config that does NOT pass ValidateConfig.
func freshConfig() *config.AppConfig {
	return &config.AppConfig{App: "test", Env: "dev"}
}

// validBaseConfig returns a minimal config that passes ValidateConfig.
func validBaseConfig() *config.AppConfig {
	return &config.AppConfig{
		App: "test",
		Env: "dev",
		Providers: config.ProvidersDef{
			Compute: "hetzner",
		},
		Servers: map[string]config.ServerDef{
			"master": {Type: "cax11", Region: "nbg1", Role: "master"},
		},
		Services: map[string]config.ServiceDef{},
	}
}

// mustValidate asserts the config passes ValidateConfig.
func mustValidate(t *testing.T, cfg *config.AppConfig) {
	t.Helper()
	if err := reconcile.ValidateConfig(cfg); err != nil {
		t.Fatalf("expected valid config, got: %v", err)
	}
}

// mustFailValidation asserts ValidateConfig fails with an error containing substr.
func mustFailValidation(t *testing.T, cfg *config.AppConfig, substr string) {
	t.Helper()
	err := reconcile.ValidateConfig(cfg)
	if err == nil {
		t.Fatalf("expected validation error containing %q, got nil", substr)
	}
	if !strings.Contains(err.Error(), substr) {
		t.Fatalf("expected error containing %q, got: %v", substr, err)
	}
}
