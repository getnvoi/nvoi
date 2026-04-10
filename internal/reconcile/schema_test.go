package reconcile

import (
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
)

func TestParseAppConfig_InvalidYAML(t *testing.T) {
	_, err := config.ParseAppConfig([]byte("not: [valid: yaml"))
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestParseAppConfig_Valid(t *testing.T) {
	cfg, err := config.ParseAppConfig([]byte("app: test\nenv: prod\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.App != "test" || cfg.Env != "prod" {
		t.Errorf("got app=%q env=%q", cfg.App, cfg.Env)
	}
}
