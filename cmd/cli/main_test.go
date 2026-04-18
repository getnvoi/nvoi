package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── Config loading ──────────────────────────────────────────────────────────

func TestMissingConfig(t *testing.T) {
	cmd := rootCmd()
	cmd.SetArgs([]string{"deploy", "--config", "/nonexistent/nvoi.yaml"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no such file") {
		t.Fatalf("error = %q, want file-not-found error", err.Error())
	}
}

// ── Dispatch: deploy ────────────────────────────────────────────────────────

func TestDeploy_ValidationError(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "nvoi.yaml")
	// Config with app+env but no providers.infra — ValidateConfig rejects it.
	if err := os.WriteFile(cfgPath, []byte("app: test\nenv: dev\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := rootCmd()
	cmd.SetArgs([]string{"deploy", "--config", cfgPath})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "providers.infra is required") {
		t.Fatalf("error = %q, want validation error from deploy path", err.Error())
	}
}

// ── Dispatch: teardown ──────────────────────────────────────────────────────

func TestTeardown_NoProvider(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "nvoi.yaml")
	if err := os.WriteFile(cfgPath, []byte("app: test\nenv: dev\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Teardown calls core.Teardown which needs a compute provider.
	cmd := rootCmd()
	cmd.SetArgs([]string{"teardown", "--config", cfgPath})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error (no compute provider)")
	}
}
