package reconcile

import (
	"context"
	"testing"
)

func TestIngress_FreshDeploy(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
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
	cfg := &AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
	}

	if err := Ingress(context.Background(), dc, nil, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
