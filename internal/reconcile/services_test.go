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

func TestServices_CaddyNeverOrphaned(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{"web": {Image: "nginx", Port: 80}},
	}
	live := &config.LiveState{Services: []string{"web", "caddy"}}

	if err := Services(context.Background(), dc, live, cfg, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sshCallMatches(ssh, "caddy", "delete") {
		t.Error("caddy should never be treated as orphan")
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
