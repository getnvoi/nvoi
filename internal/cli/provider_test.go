package cli

import (
	"strings"
	"testing"

	// Trigger hetzner init() — registers compute provider with schema.
	_ "github.com/getnvoi/nvoi/pkg/provider/hetzner"
)

func TestResolveProviderCredentials_FromEnv(t *testing.T) {
	t.Setenv("HETZNER_TOKEN", "test-token")

	creds, err := resolveProviderCredentials("compute", "hetzner", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds["token"] != "test-token" {
		t.Fatalf("token = %q, want %q", creds["token"], "test-token")
	}
}

func TestResolveProviderCredentials_ExplicitOverride(t *testing.T) {
	t.Setenv("HETZNER_TOKEN", "from-env")

	creds, err := resolveProviderCredentials("compute", "hetzner", []string{"HETZNER_TOKEN=from-arg"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds["token"] != "from-arg" {
		t.Fatalf("token = %q, want %q — explicit arg should override env", creds["token"], "from-arg")
	}
}

func TestResolveProviderCredentials_MissingRequired(t *testing.T) {
	// Ensure HETZNER_TOKEN is not set.
	t.Setenv("HETZNER_TOKEN", "")

	_, err := resolveProviderCredentials("compute", "hetzner", nil)
	if err == nil {
		t.Fatal("expected error for missing required credential")
	}
	if !strings.Contains(err.Error(), "missing required credential") {
		t.Fatalf("error = %q, want substring %q", err.Error(), "missing required credential")
	}
}

func TestResolveProviderCredentials_SchemaKeyOverride(t *testing.T) {
	// Override using the schema key ("token") instead of the env var name.
	creds, err := resolveProviderCredentials("compute", "hetzner", []string{"token=via-schema-key"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds["token"] != "via-schema-key" {
		t.Fatalf("token = %q, want %q", creds["token"], "via-schema-key")
	}
}

func TestResolveProviderCredentials_InvalidArgFormat(t *testing.T) {
	_, err := resolveProviderCredentials("compute", "hetzner", []string{"no-equals-sign"})
	if err == nil {
		t.Fatal("expected error for invalid arg format")
	}
	if !strings.Contains(err.Error(), "expected KEY=VALUE") {
		t.Fatalf("error = %q, want substring %q", err.Error(), "expected KEY=VALUE")
	}
}

func TestResolveProviderCredentials_UnknownProvider(t *testing.T) {
	_, err := resolveProviderCredentials("compute", "nonexistent", nil)
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("error = %q, want substring %q", err.Error(), "unsupported")
	}
}

func TestResolveProviderCredentials_UnknownKind(t *testing.T) {
	_, err := resolveProviderCredentials("bogus", "hetzner", nil)
	if err == nil {
		t.Fatal("expected error for unknown kind")
	}
	if !strings.Contains(err.Error(), "unknown provider kind") {
		t.Fatalf("error = %q, want substring %q", err.Error(), "unknown provider kind")
	}
}
