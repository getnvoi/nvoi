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
		Database: map[string]config.DatabaseDef{"main": {Image: "postgres:17", Volume: "pgdata"}},
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
		Database: map[string]config.DatabaseDef{"main": {Image: "postgres:17", Volume: "pgdata"}},
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
