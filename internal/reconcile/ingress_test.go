package reconcile

import (
	"context"
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
)

func TestIngress_FreshDeploy(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Domains: map[string][]string{"web": {"myapp.com"}},
	}

	if err := Ingress(context.Background(), dc, nil, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !sshContains(ssh, "replace", "apply") {
		t.Errorf("expected kubectl apply for ingress: %v", ssh.Calls)
	}
}

func TestIngress_NoDomains(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
	}

	if err := Ingress(context.Background(), dc, nil, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestIngress_OrphanRemovedWhenDomainsDropped(t *testing.T) {
	// Service "web" had domains in the previous deploy but no longer does.
	// cfg.Domains is empty — the orphan ingress must still be cleaned up.
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{"web": {Image: "nginx", Port: 80}},
		// No Domains — previously had {"web": ["myapp.com"]}
	}
	live := &config.LiveState{
		Domains: map[string][]string{"web": {"myapp.com"}},
	}

	if err := Ingress(context.Background(), dc, live, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !sshContains(ssh, "delete ingress") {
		t.Errorf("orphan ingress for web should be deleted when domains removed from config, calls: %v", ssh.Calls)
	}
}

func TestIngress_OrphanRemovedWhenServiceDropped(t *testing.T) {
	// Service "api" was removed entirely. "web" still has domains.
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{"web": {Image: "nginx", Port: 80}},
		Domains:  map[string][]string{"web": {"myapp.com"}},
	}
	live := &config.LiveState{
		Domains: map[string][]string{
			"web": {"myapp.com"},
			"api": {"api.myapp.com"},
		},
	}

	if err := Ingress(context.Background(), dc, live, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !sshContains(ssh, "delete ingress") {
		t.Errorf("orphan ingress for api should be deleted, calls: %v", ssh.Calls)
	}
}
