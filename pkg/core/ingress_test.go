package core

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/internal/testutil"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/provider/cloudflare"
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
			{Prefix: "delete secret caddy-origin-cert", Result: testutil.MockResult{}},
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

func TestParseIngressArgs_EdgeProxiedDefaultsFalse(t *testing.T) {
	routes, err := ParseIngressArgs([]string{"web:example.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if routes[0].EdgeProxied {
		t.Error("EdgeProxied should default to false")
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

// ── IngressSet — firewall coherence ─────────────────────────────────────────

func TestIngressSet_FailsWhenFirewallClosed(t *testing.T) {
	origDelay := kube.CaddyReloadDelay
	kube.CaddyReloadDelay = 0
	defer func() { kube.CaddyReloadDelay = origDelay }()

	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
		GetFirewallRulesFn: func(ctx context.Context, name string) (provider.PortAllowList, error) {
			return nil, nil
		},
	}

	err := IngressSet(context.Background(), IngressSetRequest{
		Cluster: ingressCluster(out, ingressSetSSH(), mock),
		Route:   IngressRouteArg{Service: "web", Domains: []string{"example.com"}},
	})
	if err == nil {
		t.Fatal("expected error when firewall has no 80/443")
	}
	if !strings.Contains(err.Error(), "does not have ports 80/443 open") {
		t.Errorf("error should mention closed ports, got: %v", err)
	}
}

func TestIngressSet_HardErrorWhenUnreachable(t *testing.T) {
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
		GetFirewallRulesFn: func(ctx context.Context, name string) (provider.PortAllowList, error) {
			return provider.PortAllowList{"22": {"0.0.0.0/0"}, "80": {"0.0.0.0/0"}, "443": {"0.0.0.0/0"}}, nil
		},
	}

	err := IngressSet(context.Background(), IngressSetRequest{
		Cluster: ingressCluster(out, ingressSetSSH(), mock),
		Route:   IngressRouteArg{Service: "web", Domains: []string{"example.com"}},
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

func TestIngressSet_ProxyWithOpenFirewall(t *testing.T) {
	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
		GetFirewallRulesFn: func(ctx context.Context, name string) (provider.PortAllowList, error) {
			return provider.PortAllowList{"22": {"0.0.0.0/0"}, "80": {"0.0.0.0/0"}, "443": {"0.0.0.0/0"}}, nil
		},
	}

	err := IngressSet(context.Background(), IngressSetRequest{
		Cluster: ingressCluster(out, ingressSetSSH(), mock),
		Route:   IngressRouteArg{Service: "web", Domains: []string{"example.com"}, EdgeProxied: true},
	})
	if err == nil {
		t.Fatal("expected error: edge overlay + open firewall")
	}
	if !strings.Contains(err.Error(), "origin directly reachable") {
		t.Errorf("error should mention direct origin reachability, got: %v", err)
	}
}

func TestIngressSet_ProxyWithCFFirewall(t *testing.T) {
	origDelay := kube.CaddyReloadDelay
	kube.CaddyReloadDelay = 0
	defer func() { kube.CaddyReloadDelay = origDelay }()

	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
		GetFirewallRulesFn: func(ctx context.Context, name string) (provider.PortAllowList, error) {
			return provider.PortAllowList{"22": {"0.0.0.0/0"}, "80": {"173.245.48.0/20"}, "443": {"173.245.48.0/20"}}, nil
		},
	}

	err := IngressSet(context.Background(), IngressSetRequest{
		Cluster: ingressCluster(out, ingressSetSSH(), mock),
		Route:   IngressRouteArg{Service: "web", Domains: []string{"example.com"}, EdgeProxied: true},
		CertPEM: "-----BEGIN CERTIFICATE-----\ntest\n-----END CERTIFICATE-----",
		KeyPEM:  "-----BEGIN EC PRIVATE KEY-----\ntest\n-----END EC PRIVATE KEY-----",
	})
	if err != nil {
		t.Fatalf("edge overlay + CF firewall should pass, got: %v", err)
	}
	foundProxied := false
	for _, msg := range out.Successes {
		if strings.Contains(msg, "edge proxied") {
			foundProxied = true
		}
	}
	if !foundProxied {
		t.Errorf("expected edge proxied success, got: %v", out.Successes)
	}
}

func TestIngressSet_ACMEClearsOwnedOriginSecret(t *testing.T) {
	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
		GetFirewallRulesFn: func(ctx context.Context, name string) (provider.PortAllowList, error) {
			return provider.PortAllowList{"80": {"0.0.0.0/0"}, "443": {"0.0.0.0/0"}}, nil
		},
	}
	origWait := waitHTTPSFunc
	waitHTTPSFunc = func(ctx context.Context, domain string) error { return nil }
	defer func() { waitHTTPSFunc = origWait }()

	ssh := ingressSetSSH()
	err := IngressSet(context.Background(), IngressSetRequest{
		Cluster: ingressCluster(out, ssh, mock),
		Route:   IngressRouteArg{Service: "web", Domains: []string{"example.com"}},
	})
	if err != nil {
		t.Fatalf("expected direct ACME path to succeed: %v", err)
	}
	foundDelete := false
	for _, call := range ssh.Calls {
		if strings.Contains(call, "delete secret caddy-origin-cert") {
			foundDelete = true
		}
	}
	if !foundDelete {
		t.Fatal("expected direct ACME path to clear owned origin secret")
	}
}

// ── IngressDelete ───────────────────────────────────────────────────────────

func TestIngressDelete_LastRoute_WipesEverything(t *testing.T) {
	origRevoke := revokeOriginCertFunc
	revokedIDs := []string{}
	revokeOriginCertFunc = func(ctx context.Context, apiKey, certID string) error {
		revokedIDs = append(revokedIDs, certID)
		return nil
	}
	defer func() { revokeOriginCertFunc = origRevoke }()

	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
	}
	ssh := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "get namespace", Result: testutil.MockResult{}},
			{Prefix: "create namespace", Result: testutil.MockResult{}},
			// ConfigMap with one TLS route — this is the last route
			{Prefix: "get configmap", Result: testutil.MockResult{Output: []byte("'example.com {\n\ttls /etc/caddy/tls/tls.crt /etc/caddy/tls/tls.key\n\treverse_proxy web.ns.svc.cluster.local:3000\n}'")}},
			// Annotation with cert ID
			{Prefix: "get secret caddy-origin-cert -o jsonpath=", Result: testutil.MockResult{Output: []byte("'cert-to-revoke'")}},
			{Prefix: "delete deployment/", Result: testutil.MockResult{}},
			{Prefix: "delete statefulset/", Result: testutil.MockResult{}},
			{Prefix: "delete service/", Result: testutil.MockResult{}},
			{Prefix: "delete configmap", Result: testutil.MockResult{}},
			{Prefix: "delete secret", Result: testutil.MockResult{}},
		},
	}

	err := IngressDelete(context.Background(), IngressDeleteRequest{
		Cluster: ingressCluster(out, ssh, mock),
		DNS:     ProviderRef{Name: "cloudflare", Creds: map[string]string{"api_key": "x", "zone_id": "z"}},
		Route:   IngressRouteArg{Service: "web", Domains: []string{"example.com"}},
	})
	if err != nil {
		t.Fatalf("expected success: %v", err)
	}
	if len(revokedIDs) != 1 || revokedIDs[0] != "cert-to-revoke" {
		t.Fatalf("expected cert revoked, got revokedIDs = %v", revokedIDs)
	}
}

func TestIngressDelete_RemovesRoute_KeepsOthers_NoReissue(t *testing.T) {
	createCalled := false
	origCreate := createOriginCertFunc
	createOriginCertFunc = func(ctx context.Context, apiKey string, domains []string) (*cloudflare.OriginCert, error) {
		createCalled = true
		return nil, fmt.Errorf("should not create")
	}
	defer func() { createOriginCertFunc = origCreate }()

	origDelay := kube.CaddyReloadDelay
	kube.CaddyReloadDelay = 0
	defer func() { kube.CaddyReloadDelay = origDelay }()

	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
	}
	// Two routes: web and api. Deleting web, api remains.
	caddyfile := "api.example.com {\n\ttls /etc/caddy/tls/tls.crt /etc/caddy/tls/tls.key\n\treverse_proxy api.ns.svc.cluster.local:8080\n}\n\nexample.com {\n\ttls /etc/caddy/tls/tls.crt /etc/caddy/tls/tls.key\n\treverse_proxy web.ns.svc.cluster.local:3000\n}"
	ssh := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "get namespace", Result: testutil.MockResult{}},
			{Prefix: "create namespace", Result: testutil.MockResult{}},
			{Prefix: "get configmap", Result: testutil.MockResult{Output: []byte("'" + caddyfile + "'")}},
			// Caddy exists — hot reload path
			{Prefix: "get deployment caddy", Result: testutil.MockResult{Output: []byte("tls")}},
			{Prefix: "get pods", Result: testutil.MockResult{Output: []byte("'caddy-abc123'")}},
			{Prefix: "exec caddy-abc123 -- sha256sum", Result: testutil.MockResult{Output: []byte("abc  /etc/caddy/Caddyfile")}},
			{Prefix: "exec caddy-abc123 -- caddy reload", Result: testutil.MockResult{}},
			{Prefix: "replace", Result: testutil.MockResult{}},
			{Prefix: "apply", Result: testutil.MockResult{}},
		},
	}

	err := IngressDelete(context.Background(), IngressDeleteRequest{
		Cluster: ingressCluster(out, ssh, mock),
		DNS:     ProviderRef{Name: "cloudflare", Creds: map[string]string{"api_key": "x", "zone_id": "z"}},
		Route:   IngressRouteArg{Service: "web", Domains: []string{"example.com"}},
	})
	if err != nil {
		t.Fatalf("expected success: %v", err)
	}
	if createCalled {
		t.Fatal("should not create/reissue cert when routes remain")
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

func TestIngressDelete_ClusterGone_StillRevokes(t *testing.T) {
	revokedIDs := []string{}
	origRevoke := revokeOriginCertFunc
	revokeOriginCertFunc = func(ctx context.Context, apiKey, certID string) error {
		revokedIDs = append(revokedIDs, certID)
		return nil
	}
	defer func() { revokeOriginCertFunc = origRevoke }()

	origFind := findOriginCertFunc
	findOriginCertFunc = func(ctx context.Context, apiKey, zoneID string, hostnames []string) (*cloudflare.OriginCert, error) {
		return &cloudflare.OriginCert{ID: "orphaned-cert"}, nil
	}
	defer func() { findOriginCertFunc = origFind }()

	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{Servers: nil} // ErrNoMaster

	err := IngressDelete(context.Background(), IngressDeleteRequest{
		Cluster: ingressCluster(out, nil, mock),
		DNS:     ProviderRef{Name: "cloudflare", Creds: map[string]string{"api_key": "x", "zone_id": "z"}},
		Route:   IngressRouteArg{Service: "web", Domains: []string{"example.com"}},
	})
	if err != nil {
		t.Fatalf("expected success: %v", err)
	}
	if len(revokedIDs) != 1 || revokedIDs[0] != "orphaned-cert" {
		t.Fatalf("expected orphaned cert revoked, got revokedIDs = %v", revokedIDs)
	}
}

func TestIngressDelete_RevocationFailure_PreservesLocal(t *testing.T) {
	origFind := findOriginCertFunc
	findOriginCertFunc = func(ctx context.Context, apiKey, zoneID string, hostnames []string) (*cloudflare.OriginCert, error) {
		return nil, fmt.Errorf("cloudflare api down")
	}
	defer func() { findOriginCertFunc = origFind }()

	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{Servers: nil} // ErrNoMaster — cluster gone path

	err := IngressDelete(context.Background(), IngressDeleteRequest{
		Cluster: ingressCluster(out, nil, mock),
		DNS:     ProviderRef{Name: "cloudflare", Creds: map[string]string{"api_key": "x", "zone_id": "z"}},
		Route:   IngressRouteArg{Service: "web", Domains: []string{"example.com"}},
	})
	if err == nil {
		t.Fatal("expected hard error when revocation fails")
	}
	if !strings.Contains(err.Error(), "find Origin CA cert") {
		t.Fatalf("error should mention cert lookup failure, got: %v", err)
	}
	if !strings.Contains(err.Error(), "local resources preserved") {
		t.Fatalf("error should say local resources preserved, got: %v", err)
	}
}
