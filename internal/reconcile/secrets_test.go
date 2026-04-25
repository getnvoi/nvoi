package reconcile

import (
	"context"
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
)

func TestSecrets_FreshDeploy(t *testing.T) {
	dc := testDCWithCreds(convergeMock(), "DB_PASS", "s3cret", "API_KEY", "key123")
	cfg := &config.AppConfig{Secrets: []string{"DB_PASS", "API_KEY"}}

	vals, err := Secrets(context.Background(), dc, cfg)
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
	// Secrets() does not write to k8s — it only resolves values from the source.
	ssh := convergeMock()
	dc := testDCWithCreds(ssh, "DB_PASS", "s3cret")
	cfg := &config.AppConfig{Secrets: []string{"DB_PASS"}}

	vals, err := Secrets(context.Background(), dc, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vals["DB_PASS"] != "s3cret" {
		t.Errorf("DB_PASS = %q, want s3cret", vals["DB_PASS"])
	}
	// No SSH calls should have been made — Secrets() never touches k8s
	if len(ssh.Calls) > 0 {
		t.Errorf("Secrets() should not make SSH calls, got: %v", ssh.Calls)
	}
}

func TestSecrets_MissingFromSource(t *testing.T) {
	dc := testDCWithCreds(convergeMock())
	cfg := &config.AppConfig{Secrets: []string{"MISSING"}}

	_, err := Secrets(context.Background(), dc, cfg)
	if err == nil || !strings.Contains(err.Error(), "MISSING") {
		t.Fatalf("expected error for missing secret, got: %v", err)
	}
}

func TestSecrets_AlreadyConverged(t *testing.T) {
	dc := testDCWithCreds(convergeMock(), "DB_PASS", "s3cret")
	cfg := &config.AppConfig{Secrets: []string{"DB_PASS"}}

	vals, err := Secrets(context.Background(), dc, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vals["DB_PASS"] != "s3cret" {
		t.Errorf("DB_PASS = %q, want s3cret (idempotent)", vals["DB_PASS"])
	}
}

func TestSecrets_CollectsPerServiceKeys(t *testing.T) {
	dc := testDCWithCreds(convergeMock(), "WEB_SECRET", "webval")
	cfg := &config.AppConfig{
		Services: map[string]config.ServiceDef{
			"web": {Image: "nginx", Secrets: []string{"WEB_SECRET"}},
		},
	}

	vals, err := Secrets(context.Background(), dc, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vals["WEB_SECRET"] != "webval" {
		t.Errorf("expected WEB_SECRET=webval, got %q", vals["WEB_SECRET"])
	}
}

func TestSecrets_SkipsEntriesWithEquals(t *testing.T) {
	dc := testDCWithCreds(convergeMock(), "PLAIN_KEY", "plainval")
	cfg := &config.AppConfig{
		Services: map[string]config.ServiceDef{
			"web": {Image: "nginx", Secrets: []string{
				"DATABASE_URL=$MAIN_DATABASE_URL",
				"PLAIN_KEY",
			}},
		},
	}

	vals, err := Secrets(context.Background(), dc, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vals["PLAIN_KEY"] != "plainval" {
		t.Errorf("expected PLAIN_KEY=plainval, got %q", vals["PLAIN_KEY"])
	}
	if _, ok := vals["MAIN_DATABASE_URL"]; ok {
		t.Error("entry with = should not be collected from source")
	}
	if _, ok := vals["DATABASE_URL"]; ok {
		t.Error("envName from = entry should not be collected from source")
	}
}

func TestSecrets_MissingPerServiceKey_SkippedAtCollection(t *testing.T) {
	dc := testDCWithCreds(convergeMock())
	cfg := &config.AppConfig{
		Services: map[string]config.ServiceDef{
			"web": {Image: "nginx", Secrets: []string{"MISSING_KEY"}},
		},
	}

	vals, err := Secrets(context.Background(), dc, cfg)
	if err != nil {
		t.Fatalf("Secrets should not error on missing per-service key: %v", err)
	}
	if _, ok := vals["MISSING_KEY"]; ok {
		t.Error("MISSING_KEY should not be in secretValues")
	}
}

func TestSecrets_MissingGlobalKey_Errors(t *testing.T) {
	dc := testDCWithCreds(convergeMock())
	cfg := &config.AppConfig{
		Secrets: []string{"GLOBAL_MISSING"},
	}

	_, err := Secrets(context.Background(), dc, cfg)
	if err == nil || !strings.Contains(err.Error(), "GLOBAL_MISSING") {
		t.Fatalf("expected error for missing global secret, got: %v", err)
	}
}

// TestSecrets_CollectsDatabaseCredentialVars locks the bug fix for
// `bin/deploy → databases.main.user: $MAIN_POSTGRES_USER is not a known
// env var`. databases.X.credentials.{user,password,database} carry
// $VAR refs that resolve against the same sources map services use,
// so the collector must walk them or the secrets backend never gets
// queried. The CLI side already collected these (cmd/cli/database.go);
// the reconcile-deploy side was missed — same bug class as #69's
// ae93378 fix on the resolution side, but on the collection side.
func TestSecrets_CollectsDatabaseCredentialVars(t *testing.T) {
	dc := testDCWithCreds(convergeMock(),
		"MAIN_POSTGRES_USER", "appuser",
		"MAIN_POSTGRES_PASSWORD", "s3cret",
		"MAIN_POSTGRES_DB", "myapp",
	)
	cfg := &config.AppConfig{
		Databases: map[string]config.DatabaseDef{
			"main": {
				Engine: "postgres",
				Server: "db-master",
				Size:   20,
				Credentials: &config.DatabaseCredentialsDef{
					User:     "$MAIN_POSTGRES_USER",
					Password: "$MAIN_POSTGRES_PASSWORD",
					Database: "$MAIN_POSTGRES_DB",
				},
			},
		},
	}

	vals, err := Secrets(context.Background(), dc, cfg)
	if err != nil {
		t.Fatalf("Secrets: %v", err)
	}
	for k, want := range map[string]string{
		"MAIN_POSTGRES_USER":     "appuser",
		"MAIN_POSTGRES_PASSWORD": "s3cret",
		"MAIN_POSTGRES_DB":       "myapp",
	} {
		if vals[k] != want {
			t.Errorf("vals[%q] = %q, want %q — collector missed databases.X.credentials", k, vals[k], want)
		}
	}
}

// SaaS engines have no credentials block. The collector must skip them
// without panicking on the nil pointer.
func TestSecrets_SaaSDatabaseSkippedNoPanic(t *testing.T) {
	dc := testDCWithCreds(convergeMock())
	cfg := &config.AppConfig{
		Databases: map[string]config.DatabaseDef{
			"analytics": {Engine: "neon", Region: "eu-central-1"},
		},
	}
	if _, err := Secrets(context.Background(), dc, cfg); err != nil {
		t.Fatalf("SaaS database with no credentials block must not error: %v", err)
	}
}

// TestSecrets_CollectsServiceEnvVars locks the symmetric gap on
// services.X.env: entries — the reconciler resolves env entries against
// the same sources map (services.go calls resolveEntry → resolveRef),
// so the collector must walk RHS $VAR refs there too. Same bug class
// as the database fields.
func TestSecrets_CollectsServiceEnvVars(t *testing.T) {
	dc := testDCWithCreds(convergeMock(), "EMAIL_HOST", "smtp.example.com")
	cfg := &config.AppConfig{
		Services: map[string]config.ServiceDef{
			"api": {
				Image: "nginx",
				Env:   []string{"SMTP_HOST=$EMAIL_HOST"},
			},
		},
	}

	vals, err := Secrets(context.Background(), dc, cfg)
	if err != nil {
		t.Fatalf("Secrets: %v", err)
	}
	if vals["EMAIL_HOST"] != "smtp.example.com" {
		t.Errorf("vals[EMAIL_HOST] = %q, want %q — collector missed services.X.env", vals["EMAIL_HOST"], "smtp.example.com")
	}
}

// TestSecrets_CollectsCronEnvVars — same symmetry for crons.X.env.
func TestSecrets_CollectsCronEnvVars(t *testing.T) {
	dc := testDCWithCreds(convergeMock(), "REPORT_BUCKET", "nightly-reports")
	cfg := &config.AppConfig{
		Crons: map[string]config.CronDef{
			"nightly": {
				Image:    "busybox",
				Schedule: "0 3 * * *",
				Env:      []string{"BUCKET=$REPORT_BUCKET"},
			},
		},
	}

	vals, err := Secrets(context.Background(), dc, cfg)
	if err != nil {
		t.Fatalf("Secrets: %v", err)
	}
	if vals["REPORT_BUCKET"] != "nightly-reports" {
		t.Errorf("vals[REPORT_BUCKET] = %q, want %q — collector missed crons.X.env", vals["REPORT_BUCKET"], "nightly-reports")
	}
}
