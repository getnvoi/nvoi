package reconcile

import (
	"context"
	"strings"
	"testing"
)

func TestSecrets_FreshDeploy(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &AppConfig{Secrets: []string{"DB_PASS", "API_KEY"}}
	v := testViper("DB_PASS", "s3cret", "API_KEY", "key123")

	if err := Secrets(context.Background(), dc, nil, cfg, v); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !uploadContains(ssh, "DB_PASS") {
		t.Error("DB_PASS not set")
	}
	if !uploadContains(ssh, "API_KEY") {
		t.Error("API_KEY not set")
	}
}

func TestSecrets_OrphanRemoved(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	live := &LiveState{Secrets: []string{"DB_PASS", "STALE_KEY"}}
	cfg := &AppConfig{Secrets: []string{"DB_PASS"}}
	v := testViper("DB_PASS", "s3cret")

	if err := Secrets(context.Background(), dc, live, cfg, v); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !uploadContains(ssh, "DB_PASS") {
		t.Error("DB_PASS not set")
	}
}

func TestSecrets_MissingFromEnv(t *testing.T) {
	dc := testDC(convergeMock())
	cfg := &AppConfig{Secrets: []string{"MISSING"}}

	err := Secrets(context.Background(), dc, nil, cfg, testViper())
	if err == nil || !strings.Contains(err.Error(), "MISSING") {
		t.Fatalf("expected error for missing secret, got: %v", err)
	}
}

func TestSecrets_AlreadyConverged(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	live := &LiveState{Secrets: []string{"DB_PASS"}}
	cfg := &AppConfig{Secrets: []string{"DB_PASS"}}
	v := testViper("DB_PASS", "s3cret")

	if err := Secrets(context.Background(), dc, live, cfg, v); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !uploadContains(ssh, "DB_PASS") {
		t.Error("DB_PASS should still be set (idempotent)")
	}
}
