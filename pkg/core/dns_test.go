package core

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/internal/testutil"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

func TestRemoveRouteMatching(t *testing.T) {
	routes := []kube.IngressRoute{
		{Service: "web", Port: 3000, Domains: []string{"web.com"}},
		{Service: "api", Port: 8080, Domains: []string{"api.com"}},
	}
	result := removeRoute(routes, "web")
	if len(result) != 1 {
		t.Fatalf("remove matching: got %d routes, want 1", len(result))
	}
	if result[0].Service != "api" {
		t.Errorf("remove matching: remaining service = %q, want %q", result[0].Service, "api")
	}
}

func TestRemoveRouteLastReturnsNil(t *testing.T) {
	routes := []kube.IngressRoute{
		{Service: "web", Port: 3000, Domains: []string{"web.com"}},
	}
	result := removeRoute(routes, "web")
	if result != nil {
		t.Errorf("remove last: got %v, want nil", result)
	}
}

func TestRemoveRouteNotFound(t *testing.T) {
	routes := []kube.IngressRoute{
		{Service: "web", Port: 3000, Domains: []string{"web.com"}},
	}
	result := removeRoute(routes, "api")
	if len(result) != 1 {
		t.Fatalf("not found: got %d routes, want 1", len(result))
	}
	if result[0].Service != "web" {
		t.Errorf("not found: route service = %q, want %q", result[0].Service, "web")
	}
}

func TestParseIngressArgs(t *testing.T) {
	routes, err := ParseIngressArgs([]string{
		"web:example.com,www.example.com",
		"api:api.example.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("got %d routes, want 2", len(routes))
	}
	if routes[0].Service != "web" {
		t.Errorf("route[0] service = %q, want web", routes[0].Service)
	}
	if len(routes[0].Domains) != 2 {
		t.Errorf("route[0] domains = %v, want 2", routes[0].Domains)
	}
	if routes[1].Service != "api" {
		t.Errorf("route[1] service = %q, want api", routes[1].Service)
	}
}

func TestParseIngressArgs_Invalid(t *testing.T) {
	_, err := ParseIngressArgs([]string{"nodomain"})
	if err == nil {
		t.Fatal("expected error for missing colon")
	}
	_, err = ParseIngressArgs([]string{"svc:"})
	if err == nil {
		t.Fatal("expected error for empty domains")
	}
}

func TestIngressApply_HardErrorWhenUnreachable(t *testing.T) {
	// Override WaitHTTPS to simulate unreachable domain
	origWait := waitHTTPSFunc
	waitHTTPSFunc = func(ctx context.Context, domain string) error {
		return fmt.Errorf("timeout")
	}
	defer func() { waitHTTPSFunc = origWait }()

	// Override caddy reload delay
	origDelay := kube.CaddyReloadDelay
	kube.CaddyReloadDelay = 0
	defer func() { kube.CaddyReloadDelay = origDelay }()

	out := &testutil.MockOutput{}
	ssh := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "get deployment caddy", Result: testutil.MockResult{Err: fmt.Errorf("not found")}},
			{Prefix: "get configmap", Result: testutil.MockResult{Err: fmt.Errorf("not found")}},
			{Prefix: "create namespace", Result: testutil.MockResult{}},
			{Prefix: "get namespace", Result: testutil.MockResult{}},
			{Prefix: "get service web", Result: testutil.MockResult{Output: []byte("3000")}},
			{Prefix: "replace", Result: testutil.MockResult{}},
			{Prefix: "apply", Result: testutil.MockResult{}},
		},
	}

	provider.RegisterCompute("ingress-test", provider.CredentialSchema{Name: "ingress-test"}, func(creds map[string]string) provider.ComputeProvider {
		return &testutil.MockCompute{
			Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
		}
	})

	err := IngressApply(context.Background(), IngressApplyRequest{
		Cluster: Cluster{
			AppName: "test", Env: "prod",
			Provider: "ingress-test", Output: out,
			SSHFunc: func(ctx context.Context, addr string) (utils.SSHClient, error) { return ssh, nil },
		},
		Routes: []IngressRouteArg{{Service: "web", Domains: []string{"example.com"}}},
	})

	if err == nil {
		t.Fatal("expected hard error when domain unreachable")
	}
	if !strings.Contains(err.Error(), "not reachable") {
		t.Errorf("error should mention 'not reachable', got: %v", err)
	}
	if !strings.Contains(err.Error(), "firewall") {
		t.Errorf("error should mention 'firewall', got: %v", err)
	}
}
