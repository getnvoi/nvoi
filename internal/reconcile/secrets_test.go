package reconcile

import (
	"context"
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
)

func TestSecrets_FreshDeploy(t *testing.T) {
	dc := testDC(convergeMock())
	cfg := &config.AppConfig{Secrets: []string{"DB_PASS", "API_KEY"}}
	v := testViper("DB_PASS", "s3cret", "API_KEY", "key123")

	vals, err := Secrets(context.Background(), dc, nil, cfg, v)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vals["DB_PASS"] != "s3cret" {
		t.Errorf("DB_PASS = %q, want s3cret", vals["DB_PASS"])
	}
	if vals["API_KEY"] != "key123" {
		t.Errorf("API_KEY = %q, want key123", vals["API_KEY"])
	}
}

func TestSecrets_ReturnsValues_NoK8sWrite(t *testing.T) {
	// Secrets() no longer writes to the global k8s Secret.
	// It only reads from viper and returns the map.
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{Secrets: []string{"DB_PASS"}}
	v := testViper("DB_PASS", "s3cret")

	vals, err := Secrets(context.Background(), dc, nil, cfg, v)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vals["DB_PASS"] != "s3cret" {
		t.Errorf("DB_PASS = %q, want s3cret", vals["DB_PASS"])
	}
	// No SSH calls should have been made — Secrets() no longer touches k8s
	if len(ssh.Calls) > 0 {
		t.Errorf("Secrets() should not make SSH calls, got: %v", ssh.Calls)
	}
}

func TestSecrets_MissingFromEnv(t *testing.T) {
	dc := testDC(convergeMock())
	cfg := &config.AppConfig{Secrets: []string{"MISSING"}}

	_, err := Secrets(context.Background(), dc, nil, cfg, testViper())
	if err == nil || !strings.Contains(err.Error(), "MISSING") {
		t.Fatalf("expected error for missing secret, got: %v", err)
	}
}

func TestSecrets_AlreadyConverged(t *testing.T) {
	dc := testDC(convergeMock())
	live := &config.LiveState{}
	cfg := &config.AppConfig{Secrets: []string{"DB_PASS"}}
	v := testViper("DB_PASS", "s3cret")

	vals, err := Secrets(context.Background(), dc, live, cfg, v)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vals["DB_PASS"] != "s3cret" {
		t.Errorf("DB_PASS = %q, want s3cret (idempotent)", vals["DB_PASS"])
	}
}

func TestSecrets_CollectsPerServiceKeys(t *testing.T) {
	dc := testDC(convergeMock())
	cfg := &config.AppConfig{
		Services: map[string]config.ServiceDef{
			"web": {Image: "nginx", Secrets: []string{"WEB_SECRET"}},
		},
	}
	v := testViper("WEB_SECRET", "webval")

	vals, err := Secrets(context.Background(), dc, nil, cfg, v)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vals["WEB_SECRET"] != "webval" {
		t.Errorf("expected WEB_SECRET=webval, got %q", vals["WEB_SECRET"])
	}
}

func TestSecrets_SkipsEntriesWithEquals(t *testing.T) {
	dc := testDC(convergeMock())
	cfg := &config.AppConfig{
		Services: map[string]config.ServiceDef{
			"web": {Image: "nginx", Secrets: []string{
				"DATABASE_URL=$MAIN_DATABASE_URL",
				"PLAIN_KEY",
			}},
		},
	}
	v := testViper("PLAIN_KEY", "plainval")

	vals, err := Secrets(context.Background(), dc, nil, cfg, v)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vals["PLAIN_KEY"] != "plainval" {
		t.Errorf("expected PLAIN_KEY=plainval, got %q", vals["PLAIN_KEY"])
	}
	if _, ok := vals["MAIN_DATABASE_URL"]; ok {
		t.Error("entry with = should not be collected from viper")
	}
	if _, ok := vals["DATABASE_URL"]; ok {
		t.Error("envName from = entry should not be collected from viper")
	}
}

func TestSecrets_MissingPerServiceKey_SkippedAtCollection(t *testing.T) {
	dc := testDC(convergeMock())
	cfg := &config.AppConfig{
		Services: map[string]config.ServiceDef{
			"web": {Image: "nginx", Secrets: []string{"MISSING_KEY"}},
		},
	}

	vals, err := Secrets(context.Background(), dc, nil, cfg, testViper())
	if err != nil {
		t.Fatalf("Secrets should not error on missing per-service key: %v", err)
	}
	if _, ok := vals["MISSING_KEY"]; ok {
		t.Error("MISSING_KEY should not be in secretValues")
	}
}

func TestSecrets_MissingGlobalKey_Errors(t *testing.T) {
	dc := testDC(convergeMock())
	cfg := &config.AppConfig{
		Secrets: []string{"GLOBAL_MISSING"},
	}

	_, err := Secrets(context.Background(), dc, nil, cfg, testViper())
	if err == nil || !strings.Contains(err.Error(), "GLOBAL_MISSING") {
		t.Fatalf("expected error for missing global secret, got: %v", err)
	}
}
