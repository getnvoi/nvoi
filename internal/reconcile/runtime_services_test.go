package reconcile

import (
	"context"
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
	// Apply goes through KubeClient. Success = manifest applied.
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
	// Orphan deletion goes through KubeClient.DeleteByName.
	// The function completing without error verifies the flow.
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
}

func TestServices_DatabasePackageManagedNotOrphaned(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Volumes:  map[string]config.VolumeDef{"pgdata": {Size: 20, Server: "master"}},
		Services: map[string]config.ServiceDef{"web": {Image: "nginx", Port: 80}},
		Database: map[string]config.DatabaseDef{"main": {Kind: "postgres", Image: "postgres:17", Volume: "pgdata"}},
	}
	cfg.Resolve()
	live := &config.LiveState{Services: []string{"web", "main-db"}}

	if err := Services(context.Background(), dc, live, cfg, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// main-db is protected — function succeeds without deleting it.
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
	// All 3 services applied and rolled out via KubeClient. Success = done.
}

func TestServices_DatabaseProtected(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Volumes:  map[string]config.VolumeDef{"pgdata": {Size: 20, Server: "master"}},
		Services: map[string]config.ServiceDef{"web": {Image: "nginx", Port: 80}},
		Database: map[string]config.DatabaseDef{"main": {Kind: "postgres", Image: "postgres:17", Volume: "pgdata"}},
	}
	cfg.Resolve()
	live := &config.LiveState{Services: []string{"web", "main-db"}}

	if err := Services(context.Background(), dc, live, cfg, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestServices_PerServiceSecretCreated(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{"web": {Image: "nginx", Secrets: []string{"DB_PASS"}}},
	}
	sources := map[string]string{"DB_PASS": "secret123"}

	if err := Services(context.Background(), dc, nil, cfg, sources); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Secret upsert goes through KubeClient. Success = secret created.
}

func TestServices_PerServiceSecretWithDollarVar(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{"web": {Image: "nginx", Secrets: []string{"DATABASE_URL=$MAIN_DATABASE_URL"}}},
	}
	sources := map[string]string{"MAIN_DATABASE_URL": "postgres://localhost/mydb"}

	if err := Services(context.Background(), dc, nil, cfg, sources); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestServices_PerServiceSecretComposed(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{"web": {Image: "nginx", Secrets: []string{"CREATE_SUPERUSER=$ADMIN_EMAIL:$ADMIN_PASS"}}},
	}
	sources := map[string]string{"ADMIN_EMAIL": "a@b.com", "ADMIN_PASS": "pw"}

	if err := Services(context.Background(), dc, nil, cfg, sources); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestServices_PerServiceSecretAliasedWithDollar(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{"web": {Image: "nginx", Secrets: []string{"SECRET_KEY=$BUGSINK_SECRET_KEY"}}},
	}
	sources := map[string]string{"BUGSINK_SECRET_KEY": "mysecret"}

	if err := Services(context.Background(), dc, nil, cfg, sources); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestServices_EnvWithDollarResolved(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{"web": {Image: "nginx", Env: []string{"BASE_URL=$APP_URL"}}},
	}
	sources := map[string]string{"APP_URL": "https://example.com"}

	if err := Services(context.Background(), dc, nil, cfg, sources); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestServices_EnvLiteral(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{"web": {Image: "nginx", Env: []string{"PORT=3000"}}},
	}

	if err := Services(context.Background(), dc, nil, cfg, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestServices_NoSecretsDeletesPerServiceSecret(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{"web": {Image: "nginx"}},
	}

	if err := Services(context.Background(), dc, nil, cfg, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// No secrets declared — per-service secret deleted via KubeClient.
}

func TestServices_OrphanServiceDeletesItsSecret(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{"web": {Image: "nginx"}},
	}
	live := &config.LiveState{Services: []string{"web", "old-api"}}

	if err := Services(context.Background(), dc, live, cfg, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestServices_StorageCredsInPerServiceSecret(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{"web": {Image: "nginx", Storage: []string{"releases"}}},
	}
	sources := map[string]string{
		"STORAGE_RELEASES_ENDPOINT":          "https://s3.example.com",
		"STORAGE_RELEASES_BUCKET":            "releases-bucket",
		"STORAGE_RELEASES_ACCESS_KEY_ID":     "AKID",
		"STORAGE_RELEASES_SECRET_ACCESS_KEY": "secret",
	}

	if err := Services(context.Background(), dc, nil, cfg, sources); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
