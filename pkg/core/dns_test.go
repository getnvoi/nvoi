package core

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/getnvoi/nvoi/internal/testutil"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/provider/cloudflare"
	"github.com/getnvoi/nvoi/pkg/utils"
)

func dnsDeleteCluster(out *testutil.MockOutput, ssh utils.SSHClient, mock *testutil.MockCompute) Cluster {
	provName := fmt.Sprintf("dns-delete-test-%p", mock)
	provider.RegisterCompute(provName, provider.CredentialSchema{Name: provName}, func(creds map[string]string) provider.ComputeProvider {
		return mock
	})
	return Cluster{
		AppName: "test", Env: "prod",
		Provider: provName, Output: out,
		SSHFunc: func(ctx context.Context, addr string) (utils.SSHClient, error) { return ssh, nil },
	}
}

func registerMockDNS(mock *testutil.MockDNS) string {
	name := fmt.Sprintf("dns-mock-%p", mock)
	provider.RegisterDNS(name, provider.CredentialSchema{Name: name}, func(creds map[string]string) provider.DNSProvider {
		return mock
	})
	return name
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

func TestParseIngressArgs_EdgeProxiedDefaultsFalse(t *testing.T) {
	routes, err := ParseIngressArgs([]string{"web:example.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if routes[0].EdgeProxied {
		t.Error("EdgeProxied should default to false")
	}
}

func TestParseIngressArgs_NoPerRouteEdgeMode(t *testing.T) {
	routes, err := ParseIngressArgs([]string{
		"web:example.com",
		"api:api.example.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if routes[0].EdgeProxied || routes[1].EdgeProxied {
		t.Fatal("ParseIngressArgs should not infer per-route edge mode")
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

func TestDNSDelete_BlockedWhenIngressStillReferencesDomain(t *testing.T) {
	out := &testutil.MockOutput{}
	mockDNS := &testutil.MockDNS{}
	mockCompute := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
	}
	ssh := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "get configmap", Result: testutil.MockResult{Output: []byte("'example.com {\n\treverse_proxy web.ns.svc.cluster.local:3000\n}'")}},
		},
	}

	err := DNSDelete(context.Background(), DNSDeleteRequest{
		Cluster: dnsDeleteCluster(out, ssh, mockCompute),
		DNS:     ProviderRef{Name: registerMockDNS(mockDNS)},
		Service: "web",
		Domains: []string{"example.com"},
	})

	if err == nil {
		t.Fatal("expected guarded delete to fail")
	}
	if !strings.Contains(err.Error(), "ingress still references") {
		t.Fatalf("expected ingress guard error, got: %v", err)
	}
	if len(mockDNS.DeletedA) != 0 {
		t.Fatalf("dns records should not be deleted when guarded, got %v", mockDNS.DeletedA)
	}
}

func TestDNSDelete_SucceedsWhenIngressUsageGone(t *testing.T) {
	out := &testutil.MockOutput{}
	mockDNS := &testutil.MockDNS{}
	mockCompute := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
	}
	ssh := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "get configmap", Result: testutil.MockResult{Err: fmt.Errorf("not found")}},
		},
	}

	err := DNSDelete(context.Background(), DNSDeleteRequest{
		Cluster: dnsDeleteCluster(out, ssh, mockCompute),
		DNS:     ProviderRef{Name: registerMockDNS(mockDNS)},
		Service: "web",
		Domains: []string{"example.com", "www.example.com"},
	})

	if err != nil {
		t.Fatalf("dns delete should succeed once ingress is gone: %v", err)
	}
	if len(mockDNS.DeletedA) != 2 {
		t.Fatalf("deleted records = %v, want 2 domains", mockDNS.DeletedA)
	}
}

func TestDNSDelete_SameServiceDifferentDomainDoesNotFreezeDeletion(t *testing.T) {
	out := &testutil.MockOutput{}
	mockDNS := &testutil.MockDNS{}
	mockCompute := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
	}
	ssh := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "get configmap", Result: testutil.MockResult{Output: []byte("'app.example.com {\n\treverse_proxy web.ns.svc.cluster.local:3000\n}'")}},
		},
	}

	err := DNSDelete(context.Background(), DNSDeleteRequest{
		Cluster: dnsDeleteCluster(out, ssh, mockCompute),
		DNS:     ProviderRef{Name: registerMockDNS(mockDNS)},
		Service: "web",
		Domains: []string{"old-unused.example.com"},
	})

	if err != nil {
		t.Fatalf("dns delete should allow same service when requested domain is unused by ingress: %v", err)
	}
	if len(mockDNS.DeletedA) != 1 || mockDNS.DeletedA[0] != "old-unused.example.com" {
		t.Fatalf("deleted records = %v, want old-unused.example.com", mockDNS.DeletedA)
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
			{Prefix: "delete secret caddy-origin-cert", Result: testutil.MockResult{}},
			{Prefix: "replace", Result: testutil.MockResult{}},
			{Prefix: "apply", Result: testutil.MockResult{}},
		},
	}
}

func tlsSecretValueCommands(certPEM, keyPEM string) []testutil.MockPrefix {
	return []testutil.MockPrefix{
		{Prefix: "get secret caddy-origin-cert -o jsonpath='{.data.tls.crt}'", Result: testutil.MockResult{Output: []byte("'" + base64.StdEncoding.EncodeToString([]byte(certPEM)) + "'")}},
		{Prefix: "get secret caddy-origin-cert -o jsonpath='{.data.tls.key}'", Result: testutil.MockResult{Output: []byte("'" + base64.StdEncoding.EncodeToString([]byte(keyPEM)) + "'")}},
	}
}

func makeTestCert(t *testing.T, domains []string, notAfter time.Time) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: domains[0]},
		DNSNames:              domains,
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              notAfter,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
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
		Routes:  []IngressRouteArg{{Service: "web", Domains: []string{"example.com"}, EdgeProxied: true}},
	})

	if err == nil {
		t.Fatal("expected error: edge overlay + open firewall")
	}
	if !strings.Contains(err.Error(), "origin directly reachable") {
		t.Errorf("error should mention direct origin reachability, got: %v", err)
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
		t.Fatal("expected error: direct exposure + CF-only firewall")
	}
	if !strings.Contains(err.Error(), "ingress exposure is direct") {
		t.Errorf("error should mention direct exposure, got: %v", err)
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
		Routes:  []IngressRouteArg{{Service: "web", Domains: []string{"example.com"}, EdgeProxied: true}},
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
		Routes:  []IngressRouteArg{{Service: "web", Domains: []string{"example.com"}, EdgeProxied: true}},
	})

	if err == nil {
		t.Fatal("expected error when firewall closed")
	}
	if !strings.Contains(err.Error(), "firewall set cloudflare") {
		t.Errorf("edge overlay should suggest 'firewall set cloudflare', got: %v", err)
	}
}

func TestIngressApply_NoRoutesDeletesIngress(t *testing.T) {
	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
	}
	ssh := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "get namespace", Result: testutil.MockResult{}},
			{Prefix: "create namespace", Result: testutil.MockResult{}},
			{Prefix: "delete deployment/", Result: testutil.MockResult{}},
			{Prefix: "delete statefulset/", Result: testutil.MockResult{}},
			{Prefix: "delete service/", Result: testutil.MockResult{}},
			{Prefix: "delete configmap", Result: testutil.MockResult{}},
			{Prefix: "delete secret", Result: testutil.MockResult{}},
		},
	}

	err := IngressApply(context.Background(), IngressApplyRequest{
		Cluster: ingressCluster(out, ssh, mock),
		Routes:  nil,
	})
	if err != nil {
		t.Fatalf("ingress delete via empty routes: %v", err)
	}
	if len(out.Successes) == 0 || out.Successes[len(out.Successes)-1] != "ingress removed" {
		t.Fatalf("expected ingress removed success, got %v", out.Successes)
	}
}

func TestIngressApply_ProvidedCertStillChecksOpenFirewall(t *testing.T) {
	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
		GetFirewallRulesFn: func(ctx context.Context, name string) (provider.PortAllowList, error) {
			return provider.PortAllowList{
				"80":  {"173.245.48.0/20"},
				"443": {"173.245.48.0/20"},
			}, nil
		},
	}

	err := IngressApply(context.Background(), IngressApplyRequest{
		Cluster: ingressCluster(out, ingressSSH(), mock),
		Routes:  []IngressRouteArg{{Service: "web", Domains: []string{"example.com"}}},
		CertPEM: "cert",
		KeyPEM:  "key",
	})
	if err == nil {
		t.Fatal("expected provided cert with restricted firewall to fail")
	}
	if !strings.Contains(err.Error(), "ingress exposure is direct") && !strings.Contains(err.Error(), "firewall restricts") {
		t.Fatalf("expected direct exposure firewall error, got: %v", err)
	}
}

func TestIngressApply_MixedExposureModesRejected(t *testing.T) {
	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
	}
	ssh := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "get namespace", Result: testutil.MockResult{}},
			{Prefix: "create namespace", Result: testutil.MockResult{}},
		},
	}

	err := IngressApply(context.Background(), IngressApplyRequest{
		Cluster: ingressCluster(out, ssh, mock),
		Routes: []IngressRouteArg{
			{Service: "web", Domains: []string{"example.com"}},
			{Service: "web", Domains: []string{"edge.example.com"}, EdgeProxied: true},
		},
	})
	if err == nil {
		t.Fatal("expected mixed exposure modes to fail")
	}
	if !strings.Contains(err.Error(), "mixed direct and edge-proxied") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestIngressApply_OriginCertReusedWhenDomainsMatch(t *testing.T) {
	origCreate := createOriginCertFunc
	createCalled := false
	createOriginCertFunc = func(ctx context.Context, apiKey string, domains []string) (*cloudflare.OriginCert, error) {
		createCalled = true
		return nil, fmt.Errorf("should not create")
	}
	defer func() { createOriginCertFunc = origCreate }()

	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
		GetFirewallRulesFn: func(ctx context.Context, name string) (provider.PortAllowList, error) {
			return provider.PortAllowList{"80": {"173.245.48.0/20"}, "443": {"173.245.48.0/20"}}, nil
		},
	}
	cert := makeTestCert(t, []string{"example.com"}, time.Now().Add(24*time.Hour))
	ssh := ingressSSH()
	ssh.Prefixes = append(tlsSecretValueCommands(cert, "key"), ssh.Prefixes...)

	err := IngressApply(context.Background(), IngressApplyRequest{
		Cluster: ingressCluster(out, ssh, mock),
		DNS:     ProviderRef{Name: "cloudflare", Creds: map[string]string{"api_key": "x"}},
		Routes:  []IngressRouteArg{{Service: "web", Domains: []string{"example.com"}, EdgeProxied: true}},
	})
	if err != nil {
		t.Fatalf("expected reuse path to succeed: %v", err)
	}
	if createCalled {
		t.Fatal("origin cert should have been reused, not recreated")
	}
}

func TestIngressApply_OriginCertReplacedWhenDomainsChange(t *testing.T) {
	origCreate := createOriginCertFunc
	createCalled := false
	createOriginCertFunc = func(ctx context.Context, apiKey string, domains []string) (*cloudflare.OriginCert, error) {
		createCalled = true
		return &cloudflare.OriginCert{Certificate: "new-cert", PrivateKey: "new-key"}, nil
	}
	defer func() { createOriginCertFunc = origCreate }()

	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
		GetFirewallRulesFn: func(ctx context.Context, name string) (provider.PortAllowList, error) {
			return provider.PortAllowList{"80": {"173.245.48.0/20"}, "443": {"173.245.48.0/20"}}, nil
		},
	}
	cert := makeTestCert(t, []string{"example.com", "old.example.com"}, time.Now().Add(24*time.Hour))
	ssh := ingressSSH()
	ssh.Prefixes = append(tlsSecretValueCommands(cert, "old-key"), ssh.Prefixes...)

	err := IngressApply(context.Background(), IngressApplyRequest{
		Cluster: ingressCluster(out, ssh, mock),
		DNS:     ProviderRef{Name: "cloudflare", Creds: map[string]string{"api_key": "x"}},
		Routes:  []IngressRouteArg{{Service: "web", Domains: []string{"example.com"}, EdgeProxied: true}},
	})
	if err != nil {
		t.Fatalf("expected replacement path to succeed: %v", err)
	}
	if !createCalled {
		t.Fatal("origin cert should have been replaced when domains changed")
	}
}

func TestIngressApply_OriginCertReplacedWhenExpired(t *testing.T) {
	origCreate := createOriginCertFunc
	createCalled := false
	createOriginCertFunc = func(ctx context.Context, apiKey string, domains []string) (*cloudflare.OriginCert, error) {
		createCalled = true
		return &cloudflare.OriginCert{Certificate: "new-cert", PrivateKey: "new-key"}, nil
	}
	defer func() { createOriginCertFunc = origCreate }()

	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
		GetFirewallRulesFn: func(ctx context.Context, name string) (provider.PortAllowList, error) {
			return provider.PortAllowList{"80": {"173.245.48.0/20"}, "443": {"173.245.48.0/20"}}, nil
		},
	}
	cert := makeTestCert(t, []string{"example.com"}, time.Now().Add(-time.Hour))
	ssh := ingressSSH()
	ssh.Prefixes = append(tlsSecretValueCommands(cert, "old-key"), ssh.Prefixes...)

	err := IngressApply(context.Background(), IngressApplyRequest{
		Cluster: ingressCluster(out, ssh, mock),
		DNS:     ProviderRef{Name: "cloudflare", Creds: map[string]string{"api_key": "x"}},
		Routes:  []IngressRouteArg{{Service: "web", Domains: []string{"example.com"}, EdgeProxied: true}},
	})
	if err != nil {
		t.Fatalf("expected expired replacement path to succeed: %v", err)
	}
	if !createCalled {
		t.Fatal("origin cert should have been replaced when expired")
	}
}

func TestIngressApply_ACMEClearsOwnedOriginSecret(t *testing.T) {
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

	ssh := ingressSSH()
	err := IngressApply(context.Background(), IngressApplyRequest{
		Cluster: ingressCluster(out, ssh, mock),
		Routes:  []IngressRouteArg{{Service: "web", Domains: []string{"example.com"}}},
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
