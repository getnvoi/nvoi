package cloud

import (
	"testing"

	"github.com/getnvoi/nvoi/internal/reconcile"
)

func TestSecretAdd_Valid(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Secrets = append(cfg.Secrets, "JWT_SECRET")
	mustValidate(t, cfg)
}

func TestSecretAdd_InvalidName(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Secrets = append(cfg.Secrets, "invalid-name")
	err := reconcile.ValidateConfig(cfg)
	if err == nil {
		t.Fatal("expected error for invalid secret name")
	}
}

func TestSecretAdd_Duplicate(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Secrets = []string{"JWT_SECRET"}

	// Dedup logic.
	found := false
	for _, s := range cfg.Secrets {
		if s == "JWT_SECRET" {
			found = true
			break
		}
	}
	if !found {
		cfg.Secrets = append(cfg.Secrets, "JWT_SECRET")
	}

	if len(cfg.Secrets) != 1 {
		t.Fatalf("secrets = %v, want exactly one", cfg.Secrets)
	}
	mustValidate(t, cfg)
}

func TestSecretRemove(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Secrets = []string{"JWT_SECRET", "API_KEY"}

	filtered := cfg.Secrets[:0]
	for _, s := range cfg.Secrets {
		if s != "JWT_SECRET" {
			filtered = append(filtered, s)
		}
	}
	cfg.Secrets = filtered

	if len(cfg.Secrets) != 1 || cfg.Secrets[0] != "API_KEY" {
		t.Fatalf("secrets = %v, want [API_KEY]", cfg.Secrets)
	}
	mustValidate(t, cfg)
}
