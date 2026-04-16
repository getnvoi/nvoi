package reconcile

import (
	"context"
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
)

func TestIngress_FreshDeploy(t *testing.T) {
	t.Skip("TODO: needs k8s Service in fake clientset for GetServicePort")
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
	// Ingress apply goes through KubeClient.
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
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{"web": {Image: "nginx", Port: 80}},
	}
	live := &config.LiveState{
		Domains: map[string][]string{"web": {"myapp.com"}},
	}

	if err := Ingress(context.Background(), dc, live, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Orphan ingress deleted via KubeClient.DeleteIngress.
}

func TestIngress_OrphanRemovedWhenServiceDropped(t *testing.T) {
	t.Skip("TODO: needs k8s Service + Ingress in fake clientset")
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
}
