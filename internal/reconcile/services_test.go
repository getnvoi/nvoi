package reconcile

import (
	"context"
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
)

func TestServices_FreshDeploy(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{"web": {Image: "nginx", Port: 80}},
	}

	if err := Services(context.Background(), dc, nil, cfg, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !sshContains(ssh, "replace", "apply") {
		t.Errorf("expected kubectl apply/replace: %v", ssh.Calls)
	}
}

func TestServices_OrphanRemoved(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{"web": {Image: "nginx", Port: 80}},
	}
	live := &config.LiveState{Services: []string{"web", "old-api"}}

	if err := Services(context.Background(), dc, live, cfg, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !sshCallMatches(ssh, "old-api", "delete") {
		t.Errorf("orphan old-api not deleted: %v", ssh.Calls)
	}
}

func TestServices_AlreadyConverged(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{"web": {Image: "nginx", Port: 80}},
	}
	live := &config.LiveState{Services: []string{"web"}}

	if err := Services(context.Background(), dc, live, cfg, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, call := range ssh.Calls {
		if strings.Contains(call, "delete deployment") || strings.Contains(call, "delete statefulset") {
			t.Errorf("converged should have no deletes: %s", call)
		}
	}
}

func TestServices_CompleteReplacement(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{"new-api": {Image: "api:v2", Port: 8080}},
	}
	live := &config.LiveState{Services: []string{"old-web", "old-worker"}}

	if err := Services(context.Background(), dc, live, cfg, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !sshCallMatches(ssh, "old-web", "delete") {
		t.Error("old-web not deleted")
	}
	if !sshCallMatches(ssh, "old-worker", "delete") {
		t.Error("old-worker not deleted")
	}
}

func TestServices_DatabasePackageManagedNotOrphaned(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{"web": {Image: "nginx", Port: 80}},
		Database: map[string]config.DatabaseDef{"main": {Kind: "postgres", Image: "postgres:17", Volume: "pgdata"}},
	}
	// main-db is in live (created by database package) but not in cfg.Services.
	// It must NOT be deleted — it's protected.
	live := &config.LiveState{Services: []string{"web", "main-db"}}

	if err := Services(context.Background(), dc, live, cfg, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sshCallMatches(ssh, "main-db", "delete") {
		t.Error("main-db should NOT be deleted — it's managed by the database package")
	}
}

func TestServices_EveryServiceGetsRolloutWait(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{
			"api":    {Image: "api:v1", Port: 8080},
			"web":    {Image: "nginx", Port: 80},
			"worker": {Image: "worker:v1", Port: 9090},
		},
	}

	if err := Services(context.Background(), dc, nil, cfg, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// All 3 services are sorted alphabetically: api, web, worker.
	// Each must get a "get pods" call for rollout wait — not just the last.
	podChecks := 0
	for _, call := range ssh.Calls {
		if strings.Contains(call, "get pods") && strings.Contains(call, "-o json") {
			podChecks++
		}
	}
	// At minimum 3 pod checks (one per service). May be more due to stability re-checks.
	if podChecks < 3 {
		t.Errorf("expected at least 3 rollout pod checks (one per service), got %d — calls: %v", podChecks, ssh.Calls)
	}
}

func TestServices_DatabaseProtected(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{"web": {Image: "nginx", Port: 80}},
		Database: map[string]config.DatabaseDef{"main": {Kind: "postgres", Image: "postgres:17", Volume: "pgdata"}},
	}
	live := &config.LiveState{Services: []string{"web", "main-db", "stale-worker"}}

	if err := Services(context.Background(), dc, live, cfg, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sshCallMatches(ssh, "main-db", "delete") {
		t.Error("main-db should not be deleted")
	}
	if !sshCallMatches(ssh, "stale-worker", "delete") {
		t.Error("stale-worker should be deleted")
	}
}

func TestServices_PerServiceSecretCreated(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{
			"web": {Image: "nginx", Port: 80, Secrets: []string{"WEB_SECRET"}},
		},
	}
	sources := map[string]string{"WEB_SECRET": "s3cret"}

	if err := Services(context.Background(), dc, nil, cfg, sources); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should upsert WEB_SECRET into web-secrets k8s Secret
	if !uploadContains(ssh, "s3cret") {
		t.Error("WEB_SECRET value not upserted into per-service secret")
	}
}

func TestServices_PerServiceSecretWithDollarVar(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{
			"web": {Image: "nginx", Port: 80, Secrets: []string{"DATABASE_URL=$MAIN_DATABASE_URL"}},
		},
	}
	sources := map[string]string{"MAIN_DATABASE_URL": "postgresql://host/db"}

	if err := Services(context.Background(), dc, nil, cfg, sources); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Resolved value should be upserted into web-secrets
	if !uploadContains(ssh, "postgresql://host/db") {
		t.Error("resolved DATABASE_URL not upserted into per-service secret")
	}
}

func TestServices_PerServiceSecretComposed(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{
			"web": {Image: "nginx", Port: 80, Secrets: []string{"CREATE_SUPERUSER=$DB_USER:$DB_PASS"}},
		},
	}
	sources := map[string]string{"DB_USER": "admin", "DB_PASS": "secret"}

	if err := Services(context.Background(), dc, nil, cfg, sources); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !uploadContains(ssh, "admin:secret") {
		t.Error("composed value not upserted into per-service secret")
	}
}

func TestServices_PerServiceSecretAliasedWithDollar(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{
			"web": {Image: "nginx", Port: 80, Secrets: []string{"SECRET_KEY=$BUGSINK_SECRET_KEY"}},
		},
	}
	sources := map[string]string{"BUGSINK_SECRET_KEY": "keyval"}

	if err := Services(context.Background(), dc, nil, cfg, sources); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !uploadContains(ssh, "keyval") {
		t.Error("aliased secret value not upserted into per-service secret")
	}
}

func TestServices_EnvWithDollarResolved(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{
			"web": {Image: "nginx", Port: 80, Env: []string{"BASE_URL=https://$HOST/api"}},
		},
	}
	sources := map[string]string{"HOST": "example.com"}

	if err := Services(context.Background(), dc, nil, cfg, sources); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The resolved value should appear in the uploaded YAML manifest
	if !uploadContains(ssh, "https://example.com/api") {
		t.Error("resolved env var not in manifest")
	}
}

func TestServices_EnvLiteral(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{
			"web": {Image: "nginx", Port: 80, Env: []string{"FOO=bar"}},
		},
	}

	if err := Services(context.Background(), dc, nil, cfg, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !uploadContains(ssh, "bar") {
		t.Error("literal env var not in manifest")
	}
}

func TestServices_NoSecretsDeletesPerServiceSecret(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{"web": {Image: "nginx", Port: 80}},
	}

	if err := Services(context.Background(), dc, nil, cfg, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// With no secrets: field, the per-service secret should be deleted
	if !sshCallMatches(ssh, "web-secrets", "delete secret") {
		t.Error("expected web-secrets to be deleted when service has no secrets")
	}
}

func TestServices_OrphanServiceDeletesItsSecret(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{"web": {Image: "nginx", Port: 80}},
	}
	live := &config.LiveState{Services: []string{"web", "old-api"}}

	if err := Services(context.Background(), dc, live, cfg, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// old-api is orphaned — its per-service secret should be cleaned up
	if !sshCallMatches(ssh, "old-api-secrets", "delete secret") {
		t.Error("orphan old-api's per-service secret not cleaned up")
	}
}

func TestServices_StorageCredsInPerServiceSecret(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Storage:  map[string]config.StorageDef{"assets": {}},
		Services: map[string]config.ServiceDef{"web": {Image: "nginx", Port: 80, Storage: []string{"assets"}}},
	}
	sources := map[string]string{
		"STORAGE_ASSETS_ENDPOINT":          "https://s3.example.com",
		"STORAGE_ASSETS_BUCKET":            "nvoi-myapp-prod-assets",
		"STORAGE_ASSETS_ACCESS_KEY_ID":     "AKID",
		"STORAGE_ASSETS_SECRET_ACCESS_KEY": "SECRET",
	}

	if err := Services(context.Background(), dc, nil, cfg, sources); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Storage credentials should be upserted into web-secrets
	if !uploadContains(ssh, "AKID") {
		t.Error("storage access key not in per-service secret")
	}
	if !uploadContains(ssh, "SECRET") {
		t.Error("storage secret key not in per-service secret")
	}
}

func TestServices_NoAutoInjection(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{"web": {Image: "nginx", Port: 80}},
	}
	sources := map[string]string{"MAIN_DATABASE_URL": "postgresql://host/db"}

	if err := Services(context.Background(), dc, nil, cfg, sources); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Source values should NOT be auto-injected if service doesn't reference them
	if uploadContains(ssh, "MAIN_DATABASE_URL") {
		t.Error("source values should not be auto-injected — service must declare them explicitly")
	}
}
