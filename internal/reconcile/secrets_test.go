package reconcile

import (
	"context"
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
)

func TestSecrets_FreshDeploy(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{Secrets: []string{"DB_PASS", "API_KEY"}}
	v := testViper("DB_PASS", "s3cret", "API_KEY", "key123")

	vals, err := Secrets(context.Background(), dc, nil, cfg, v)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !uploadContains(ssh, "DB_PASS") {
		t.Error("DB_PASS not set")
	}
	if !uploadContains(ssh, "API_KEY") {
		t.Error("API_KEY not set")
	}
	if vals["DB_PASS"] != "s3cret" {
		t.Errorf("expected DB_PASS=s3cret, got %q", vals["DB_PASS"])
	}
	if vals["API_KEY"] != "key123" {
		t.Errorf("expected API_KEY=key123, got %q", vals["API_KEY"])
	}
}

func TestSecrets_OrphanRemoved(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	live := &config.LiveState{Secrets: []string{"DB_PASS", "STALE_KEY"}}
	cfg := &config.AppConfig{Secrets: []string{"DB_PASS"}}
	v := testViper("DB_PASS", "s3cret")

	if _, err := Secrets(context.Background(), dc, live, cfg, v); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !uploadContains(ssh, "DB_PASS") {
		t.Error("DB_PASS not set")
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
	ssh := convergeMock()
	dc := testDC(ssh)
	live := &config.LiveState{Secrets: []string{"DB_PASS"}}
	cfg := &config.AppConfig{Secrets: []string{"DB_PASS"}}
	v := testViper("DB_PASS", "s3cret")

	if _, err := Secrets(context.Background(), dc, live, cfg, v); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !uploadContains(ssh, "DB_PASS") {
		t.Error("DB_PASS should still be set (idempotent)")
	}
}

func TestSecrets_CollectsPerServiceKeys(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
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
	// Entries with = always have $ (enforced by validation).
	// They resolve from sources later, not from viper.
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		Services: map[string]config.ServiceDef{
			"web": {Image: "nginx", Secrets: []string{
				"DATABASE_URL=$MAIN_DATABASE_URL", // has = → skipped in viper collection
				"PLAIN_KEY",                       // bare → read from viper
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
	// Entries with = should NOT be collected from viper
	if _, ok := vals["MAIN_DATABASE_URL"]; ok {
		t.Error("entry with = should not be collected from viper")
	}
	if _, ok := vals["DATABASE_URL"]; ok {
		t.Error("envName from = entry should not be collected from viper")
	}
}

func TestSecrets_MissingPerServiceKey(t *testing.T) {
	dc := testDC(convergeMock())
	cfg := &config.AppConfig{
		Services: map[string]config.ServiceDef{
			"web": {Image: "nginx", Secrets: []string{"MISSING_KEY"}},
		},
	}

	_, err := Secrets(context.Background(), dc, nil, cfg, testViper())
	if err == nil || !strings.Contains(err.Error(), "MISSING_KEY") {
		t.Fatalf("expected error for missing per-service secret, got: %v", err)
	}
}
