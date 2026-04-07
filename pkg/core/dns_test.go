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

func ingressCluster(out *testutil.MockOutput, ssh utils.SSHClient, mock *testutil.MockCompute) Cluster {
	provName := fmt.Sprintf("ingress-test-%p", mock) // unique per mock to avoid registration collisions
	provider.RegisterCompute(provName, provider.CredentialSchema{Name: provName}, func(creds map[string]string) provider.ComputeProvider {
		return mock
	})
	return Cluster{
		AppName: "test", Env: "prod",
		Provider: provName, Output: out,
		SSHFunc: func(ctx context.Context, addr string) (utils.SSHClient, error) { return ssh, nil },
	}
}

func ingressSSH() *testutil.MockSSH {
	return &testutil.MockSSH{
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
}

func TestIngressApply_FailsWhenFirewallClosed(t *testing.T) {
	origDelay := kube.CaddyReloadDelay
	kube.CaddyReloadDelay = 0
	defer func() { kube.CaddyReloadDelay = origDelay }()

	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
		// GetFirewallRules returns nil — no 80/443 open
		GetFirewallRulesFn: func(ctx context.Context, name string) (provider.PortAllowList, error) {
			return nil, nil
		},
	}

	err := IngressApply(context.Background(), IngressApplyRequest{
		Cluster: ingressCluster(out, ingressSSH(), mock),
		Routes:  []IngressRouteArg{{Service: "web", Domains: []string{"example.com"}}},
	})

	if err == nil {
		t.Fatal("expected error when firewall has no 80/443")
	}
	if !strings.Contains(err.Error(), "does not have ports 80/443 open") {
		t.Errorf("error should mention closed ports, got: %v", err)
	}
	if !strings.Contains(err.Error(), "firewall set default") {
		t.Errorf("error should suggest fix, got: %v", err)
	}
}

func TestIngressApply_HardErrorWhenUnreachable(t *testing.T) {
	origWait := waitHTTPSFunc
	waitHTTPSFunc = func(ctx context.Context, domain string) error {
		return fmt.Errorf("timeout")
	}
	defer func() { waitHTTPSFunc = origWait }()

	origDelay := kube.CaddyReloadDelay
	kube.CaddyReloadDelay = 0
	defer func() { kube.CaddyReloadDelay = origDelay }()

	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
		// Firewall has 80/443 open — passes pre-flight
		GetFirewallRulesFn: func(ctx context.Context, name string) (provider.PortAllowList, error) {
			return provider.PortAllowList{
				"22":  {"0.0.0.0/0"},
				"80":  {"0.0.0.0/0"},
				"443": {"0.0.0.0/0"},
			}, nil
		},
	}

	err := IngressApply(context.Background(), IngressApplyRequest{
		Cluster: ingressCluster(out, ingressSSH(), mock),
		Routes:  []IngressRouteArg{{Service: "web", Domains: []string{"example.com"}}},
	})

	if err == nil {
		t.Fatal("expected hard error when domain unreachable")
	}
	if !strings.Contains(err.Error(), "not reachable") {
		t.Errorf("error should mention 'not reachable', got: %v", err)
	}
}

func TestIngressApply_NoCluster(t *testing.T) {
	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: nil, // no servers — Master() will fail
	}

	err := IngressApply(context.Background(), IngressApplyRequest{
		Cluster: ingressCluster(out, nil, mock),
		Routes:  []IngressRouteArg{{Service: "web", Domains: []string{"example.com"}}},
	})

	if err == nil {
		t.Fatal("expected error when no cluster exists")
	}
}

func TestIngressApply_ProxyWithOpenFirewall(t *testing.T) {
	origDelay := kube.CaddyReloadDelay
	kube.CaddyReloadDelay = 0
	defer func() { kube.CaddyReloadDelay = origDelay }()

	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
		GetFirewallRulesFn: func(ctx context.Context, name string) (provider.PortAllowList, error) {
			return provider.PortAllowList{
				"22":  {"0.0.0.0/0"},
				"80":  {"0.0.0.0/0"},
				"443": {"0.0.0.0/0"},
			}, nil
		},
	}

	err := IngressApply(context.Background(), IngressApplyRequest{
		Cluster: ingressCluster(out, ingressSSH(), mock),
		Routes:  []IngressRouteArg{{Service: "web", Domains: []string{"example.com"}, Proxy: true}},
	})

	if err == nil {
		t.Fatal("expected error: proxy + open firewall")
	}
	if !strings.Contains(err.Error(), "bypassing Cloudflare") {
		t.Errorf("error should mention bypassing Cloudflare, got: %v", err)
	}
}

func TestIngressApply_NoProxyWithCFFirewall(t *testing.T) {
	origDelay := kube.CaddyReloadDelay
	kube.CaddyReloadDelay = 0
	defer func() { kube.CaddyReloadDelay = origDelay }()

	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
		GetFirewallRulesFn: func(ctx context.Context, name string) (provider.PortAllowList, error) {
			return provider.PortAllowList{
				"22":  {"0.0.0.0/0"},
				"80":  {"173.245.48.0/20", "103.21.244.0/22"},
				"443": {"173.245.48.0/20", "103.21.244.0/22"},
			}, nil
		},
	}

	err := IngressApply(context.Background(), IngressApplyRequest{
		Cluster: ingressCluster(out, ingressSSH(), mock),
		Routes:  []IngressRouteArg{{Service: "web", Domains: []string{"example.com"}}},
	})

	if err == nil {
		t.Fatal("expected error: no proxy + CF-only firewall")
	}
	if !strings.Contains(err.Error(), "ACME") {
		t.Errorf("error should mention ACME, got: %v", err)
	}
}

func TestIngressApply_ProxyWithCFFirewall(t *testing.T) {
	origDelay := kube.CaddyReloadDelay
	kube.CaddyReloadDelay = 0
	defer func() { kube.CaddyReloadDelay = origDelay }()

	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
		GetFirewallRulesFn: func(ctx context.Context, name string) (provider.PortAllowList, error) {
			return provider.PortAllowList{
				"22":  {"0.0.0.0/0"},
				"80":  {"173.245.48.0/20"},
				"443": {"173.245.48.0/20"},
			}, nil
		},
	}

	err := IngressApply(context.Background(), IngressApplyRequest{
		Cluster: ingressCluster(out, ingressSSH(), mock),
		Routes:  []IngressRouteArg{{Service: "web", Domains: []string{"example.com"}, Proxy: true}},
	})

	if err != nil {
		t.Fatalf("proxy + CF firewall should pass, got: %v", err)
	}
	// Should report proxied success, not WaitHTTPS
	foundProxied := false
	for _, msg := range out.Successes {
		if strings.Contains(msg, "proxied via Cloudflare") {
			foundProxied = true
		}
	}
	if !foundProxied {
		t.Errorf("expected 'proxied via Cloudflare' success, got: %v", out.Successes)
	}
}

func TestIngressApply_FirewallClosedWithProxy(t *testing.T) {
	origDelay := kube.CaddyReloadDelay
	kube.CaddyReloadDelay = 0
	defer func() { kube.CaddyReloadDelay = origDelay }()

	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
		GetFirewallRulesFn: func(ctx context.Context, name string) (provider.PortAllowList, error) {
			return nil, nil // no rules at all
		},
	}

	err := IngressApply(context.Background(), IngressApplyRequest{
		Cluster: ingressCluster(out, ingressSSH(), mock),
		Routes:  []IngressRouteArg{{Service: "web", Domains: []string{"example.com"}, Proxy: true}},
	})

	if err == nil {
		t.Fatal("expected error when firewall closed")
	}
	if !strings.Contains(err.Error(), "firewall set cloudflare") {
		t.Errorf("proxy mode should suggest 'firewall set cloudflare', got: %v", err)
	}
}
