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

// ── Helpers ─────────────────────────────────────────────────────────────────

func ingressCluster(out *testutil.MockOutput, ssh utils.SSHClient, mock *testutil.MockCompute) Cluster {
	provName := fmt.Sprintf("ingress-test-%p", mock)
	provider.RegisterCompute(provName, provider.CredentialSchema{Name: provName}, func(creds map[string]string) provider.ComputeProvider {
		return mock
	})
	return Cluster{
		AppName: "test", Env: "prod",
		Provider: provName, Output: out,
		SSHFunc: func(ctx context.Context, addr string) (utils.SSHClient, error) { return ssh, nil },
	}
}

func ingressSetSSH() *testutil.MockSSH {
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

// ── ParseIngressArgs ────────────────────────────────────────────────────────

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

// ── IngressSet — ACME ──────────────────────────────────────────────────────

func TestIngressSet_HardErrorWhenUnreachable(t *testing.T) {
	origDelay := kube.CaddyReloadDelay
	kube.CaddyReloadDelay = 0
	defer func() { kube.CaddyReloadDelay = origDelay }()

	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
	}

	err := IngressSet(context.Background(), IngressSetRequest{
		Cluster: ingressCluster(out, ingressSetSSH(), mock),
		Route:   IngressRouteArg{Service: "web", Domains: []string{"example.com"}},
		Hooks: &IngressHooks{
			WaitHTTPS: func(ctx context.Context, domain string) error { return fmt.Errorf("timeout") },
		},
	})
	if err == nil {
		t.Fatal("expected hard error when domain unreachable")
	}
	if !strings.Contains(err.Error(), "not reachable") {
		t.Errorf("error should mention 'not reachable', got: %v", err)
	}
}

func TestIngressSet_NoCluster(t *testing.T) {
	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{Servers: nil}

	err := IngressSet(context.Background(), IngressSetRequest{
		Cluster: ingressCluster(out, nil, mock),
		Route:   IngressRouteArg{Service: "web", Domains: []string{"example.com"}},
	})
	if err == nil {
		t.Fatal("expected error when no cluster exists")
	}
}

func TestIngressSet_ACMEPath(t *testing.T) {
	origDelay := kube.CaddyReloadDelay
	kube.CaddyReloadDelay = 0
	defer func() { kube.CaddyReloadDelay = origDelay }()

	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
	}
	ssh := ingressSetSSH()
	err := IngressSet(context.Background(), IngressSetRequest{
		Cluster: ingressCluster(out, ssh, mock),
		Route:   IngressRouteArg{Service: "web", Domains: []string{"example.com"}},
		Hooks:   &IngressHooks{WaitHTTPS: func(ctx context.Context, domain string) error { return nil }},
	})
	if err != nil {
		t.Fatalf("expected ACME path to succeed: %v", err)
	}
}

// ── IngressDelete ───────────────────────────────────────────────────────────

func TestIngressDelete_LastRoute_WipesEverything(t *testing.T) {
	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
	}
	ssh := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "get namespace", Result: testutil.MockResult{}},
			{Prefix: "create namespace", Result: testutil.MockResult{}},
			// ConfigMap with one route — this is the last route
			{Prefix: "get configmap", Result: testutil.MockResult{Output: []byte("'example.com {\n\treverse_proxy web.ns.svc.cluster.local:3000\n}'")}},
			{Prefix: "delete deployment/", Result: testutil.MockResult{}},
			{Prefix: "delete statefulset/", Result: testutil.MockResult{}},
			{Prefix: "delete service/", Result: testutil.MockResult{}},
			{Prefix: "delete configmap", Result: testutil.MockResult{}},
		},
	}

	err := IngressDelete(context.Background(), IngressDeleteRequest{
		Cluster: ingressCluster(out, ssh, mock),
		Route:   IngressRouteArg{Service: "web", Domains: []string{"example.com"}},
	})
	if err != nil {
		t.Fatalf("expected success: %v", err)
	}
}

func TestIngressDelete_RemovesRoute_KeepsOthers(t *testing.T) {
	origDelay := kube.CaddyReloadDelay
	kube.CaddyReloadDelay = 0
	defer func() { kube.CaddyReloadDelay = origDelay }()

	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
	}
	// Two routes: web and api. Deleting web, api remains.
	caddyfile := "api.example.com {\n\treverse_proxy api.ns.svc.cluster.local:8080\n}\n\nexample.com {\n\treverse_proxy web.ns.svc.cluster.local:3000\n}"
	ssh := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "get namespace", Result: testutil.MockResult{}},
			{Prefix: "create namespace", Result: testutil.MockResult{}},
			{Prefix: "get configmap", Result: testutil.MockResult{Output: []byte("'" + caddyfile + "'")}},
			// Caddy exists — hot reload path
			{Prefix: "get deployment caddy", Result: testutil.MockResult{Output: []byte("caddy")}},
			{Prefix: "get pods", Result: testutil.MockResult{Output: []byte("'caddy-abc123'")}},
			{Prefix: "exec caddy-abc123 -- sha256sum", Result: testutil.MockResult{Output: []byte("abc  /etc/caddy/Caddyfile")}},
			{Prefix: "exec caddy-abc123 -- caddy reload", Result: testutil.MockResult{}},
			{Prefix: "replace", Result: testutil.MockResult{}},
			{Prefix: "apply", Result: testutil.MockResult{}},
		},
	}

	err := IngressDelete(context.Background(), IngressDeleteRequest{
		Cluster: ingressCluster(out, ssh, mock),
		Route:   IngressRouteArg{Service: "web", Domains: []string{"example.com"}},
	})
	if err != nil {
		t.Fatalf("expected success: %v", err)
	}
	// Verify Caddy was redeployed (not wiped)
	foundCaddyUpdate := false
	for _, msg := range out.Successes {
		if strings.Contains(msg, "caddy updated") {
			foundCaddyUpdate = true
		}
	}
	if !foundCaddyUpdate {
		t.Fatalf("expected caddy updated, got successes: %v", out.Successes)
	}
}

func TestIngressDelete_ClusterGone(t *testing.T) {
	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{Servers: nil} // ErrNoMaster

	err := IngressDelete(context.Background(), IngressDeleteRequest{
		Cluster: ingressCluster(out, nil, mock),
		Route:   IngressRouteArg{Service: "web", Domains: []string{"example.com"}},
	})
	if err != nil {
		t.Fatalf("expected success: %v", err)
	}
}
