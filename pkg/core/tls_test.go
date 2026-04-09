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
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/provider/cloudflare"
)

// ── Helpers ─────────────────────────────────────────────────────────────────

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

// ── Origin cert reuse ───────────────────────────────────────────────────────

func TestIngressSet_OriginCertReusedWhenDomainsMatch(t *testing.T) {
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
	ssh := ingressSetSSH()
	ssh.Prefixes = append(tlsSecretValueCommands(cert, "key"), ssh.Prefixes...)
	ssh.Prefixes = append([]testutil.MockPrefix{
		{Prefix: "get secret caddy-origin-cert -o jsonpath='{.metadata.annotations", Result: testutil.MockResult{Output: []byte("'existing-cert-id'")}},
	}, ssh.Prefixes...)

	err := IngressSet(context.Background(), IngressSetRequest{
		Cluster: ingressCluster(out, ssh, mock),
		DNS:     ProviderRef{Name: "cloudflare", Creds: map[string]string{"api_key": "x"}},
		Route:   IngressRouteArg{Service: "web", Domains: []string{"example.com"}, EdgeProxied: true},
	})
	if err != nil {
		t.Fatalf("expected reuse path to succeed: %v", err)
	}
	if createCalled {
		t.Fatal("origin cert should have been reused, not recreated")
	}
}

func TestIngressSet_OriginCertReplacedWhenDomainsChange(t *testing.T) {
	origCreate := createOriginCertFunc
	createCalled := false
	createOriginCertFunc = func(ctx context.Context, apiKey string, domains []string) (*cloudflare.OriginCert, error) {
		createCalled = true
		return &cloudflare.OriginCert{ID: "new-id", Certificate: "new-cert", PrivateKey: "new-key"}, nil
	}
	defer func() { createOriginCertFunc = origCreate }()

	origRevoke := revokeOriginCertFunc
	revokeOriginCertFunc = func(ctx context.Context, apiKey, certID string) error { return nil }
	defer func() { revokeOriginCertFunc = origRevoke }()

	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
		GetFirewallRulesFn: func(ctx context.Context, name string) (provider.PortAllowList, error) {
			return provider.PortAllowList{"80": {"173.245.48.0/20"}, "443": {"173.245.48.0/20"}}, nil
		},
	}
	cert := makeTestCert(t, []string{"example.com", "old.example.com"}, time.Now().Add(24*time.Hour))
	ssh := ingressSetSSH()
	ssh.Prefixes = append(tlsSecretValueCommands(cert, "old-key"), ssh.Prefixes...)
	ssh.Prefixes = append([]testutil.MockPrefix{
		{Prefix: "get secret caddy-origin-cert -o jsonpath='{.metadata.annotations", Result: testutil.MockResult{Output: []byte("'old-cert-id'")}},
	}, ssh.Prefixes...)

	err := IngressSet(context.Background(), IngressSetRequest{
		Cluster: ingressCluster(out, ssh, mock),
		DNS:     ProviderRef{Name: "cloudflare", Creds: map[string]string{"api_key": "x"}},
		Route:   IngressRouteArg{Service: "web", Domains: []string{"example.com"}, EdgeProxied: true},
	})
	if err != nil {
		t.Fatalf("expected replacement path to succeed: %v", err)
	}
	if !createCalled {
		t.Fatal("origin cert should have been replaced when domains changed")
	}
}

func TestIngressSet_OriginCertReplacedWhenExpired(t *testing.T) {
	origCreate := createOriginCertFunc
	createCalled := false
	createOriginCertFunc = func(ctx context.Context, apiKey string, domains []string) (*cloudflare.OriginCert, error) {
		createCalled = true
		return &cloudflare.OriginCert{ID: "new-id", Certificate: "new-cert", PrivateKey: "new-key"}, nil
	}
	defer func() { createOriginCertFunc = origCreate }()

	origRevoke := revokeOriginCertFunc
	revokeOriginCertFunc = func(ctx context.Context, apiKey, certID string) error { return nil }
	defer func() { revokeOriginCertFunc = origRevoke }()

	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
		GetFirewallRulesFn: func(ctx context.Context, name string) (provider.PortAllowList, error) {
			return provider.PortAllowList{"80": {"173.245.48.0/20"}, "443": {"173.245.48.0/20"}}, nil
		},
	}
	cert := makeTestCert(t, []string{"example.com"}, time.Now().Add(-time.Hour))
	ssh := ingressSetSSH()
	ssh.Prefixes = append(tlsSecretValueCommands(cert, "old-key"), ssh.Prefixes...)
	ssh.Prefixes = append([]testutil.MockPrefix{
		{Prefix: "get secret caddy-origin-cert -o jsonpath='{.metadata.annotations", Result: testutil.MockResult{Output: []byte("'old-cert-id'")}},
	}, ssh.Prefixes...)

	err := IngressSet(context.Background(), IngressSetRequest{
		Cluster: ingressCluster(out, ssh, mock),
		DNS:     ProviderRef{Name: "cloudflare", Creds: map[string]string{"api_key": "x"}},
		Route:   IngressRouteArg{Service: "web", Domains: []string{"example.com"}, EdgeProxied: true},
	})
	if err != nil {
		t.Fatalf("expected expired replacement path to succeed: %v", err)
	}
	if !createCalled {
		t.Fatal("origin cert should have been replaced when expired")
	}
}

// ── Origin CA lifecycle — annotation storage ────────────────────────────────

func TestIngressSet_OriginCertIDStoredAsAnnotation(t *testing.T) {
	origCreate := createOriginCertFunc
	createOriginCertFunc = func(ctx context.Context, apiKey string, domains []string) (*cloudflare.OriginCert, error) {
		return &cloudflare.OriginCert{ID: "new-cert-id", Certificate: "new-cert", PrivateKey: "new-key"}, nil
	}
	defer func() { createOriginCertFunc = origCreate }()

	origRevoke := revokeOriginCertFunc
	revokeOriginCertFunc = func(ctx context.Context, apiKey, certID string) error { return nil }
	defer func() { revokeOriginCertFunc = origRevoke }()

	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
		GetFirewallRulesFn: func(ctx context.Context, name string) (provider.PortAllowList, error) {
			return provider.PortAllowList{"80": {"173.245.48.0/20"}, "443": {"173.245.48.0/20"}}, nil
		},
	}
	ssh := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "get secret caddy-origin-cert -o jsonpath='{.data.tls.crt}'", Result: testutil.MockResult{Err: fmt.Errorf("not found")}},
			{Prefix: "get secret caddy-origin-cert -o jsonpath=", Result: testutil.MockResult{Err: fmt.Errorf("not found")}},
			{Prefix: "get deployment caddy", Result: testutil.MockResult{Err: fmt.Errorf("not found")}},
			{Prefix: "get configmap", Result: testutil.MockResult{Err: fmt.Errorf("not found")}},
			{Prefix: "create namespace", Result: testutil.MockResult{}},
			{Prefix: "get namespace", Result: testutil.MockResult{}},
			{Prefix: "get service web", Result: testutil.MockResult{Output: []byte("3000")}},
			{Prefix: "replace", Result: testutil.MockResult{}},
			{Prefix: "apply", Result: testutil.MockResult{}},
		},
	}

	err := IngressSet(context.Background(), IngressSetRequest{
		Cluster: ingressCluster(out, ssh, mock),
		DNS:     ProviderRef{Name: "cloudflare", Creds: map[string]string{"api_key": "x", "zone_id": "z"}},
		Route:   IngressRouteArg{Service: "web", Domains: []string{"example.com"}, EdgeProxied: true},
	})
	if err != nil {
		t.Fatalf("expected success: %v", err)
	}

	foundAnnotation := false
	for _, upload := range ssh.Uploads {
		content := string(upload.Content)
		if strings.Contains(content, "nvoi/origin-ca-id") && strings.Contains(content, "new-cert-id") {
			foundAnnotation = true
		}
	}
	if !foundAnnotation {
		t.Fatal("expected TLS secret to contain nvoi/origin-ca-id annotation with cert ID")
	}
}

// ── Origin CA lifecycle — revocation on domain change ───────────────────────

func TestIngressSet_OldCertRevokedOnDomainChange(t *testing.T) {
	origCreate := createOriginCertFunc
	createOriginCertFunc = func(ctx context.Context, apiKey string, domains []string) (*cloudflare.OriginCert, error) {
		return &cloudflare.OriginCert{ID: "new-cert-id", Certificate: "new-cert", PrivateKey: "new-key"}, nil
	}
	defer func() { createOriginCertFunc = origCreate }()

	revokedIDs := []string{}
	origRevoke := revokeOriginCertFunc
	revokeOriginCertFunc = func(ctx context.Context, apiKey, certID string) error {
		revokedIDs = append(revokedIDs, certID)
		return nil
	}
	defer func() { revokeOriginCertFunc = origRevoke }()

	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
		GetFirewallRulesFn: func(ctx context.Context, name string) (provider.PortAllowList, error) {
			return provider.PortAllowList{"80": {"173.245.48.0/20"}, "443": {"173.245.48.0/20"}}, nil
		},
	}
	cert := makeTestCert(t, []string{"old.example.com"}, time.Now().Add(24*time.Hour))
	ssh := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "get secret caddy-origin-cert -o jsonpath='{.data.tls.crt}'", Result: testutil.MockResult{Output: []byte("'" + base64.StdEncoding.EncodeToString([]byte(cert)) + "'")}},
			{Prefix: "get secret caddy-origin-cert -o jsonpath='{.data.tls.key}'", Result: testutil.MockResult{Output: []byte("'" + base64.StdEncoding.EncodeToString([]byte("key")) + "'")}},
			{Prefix: "get secret caddy-origin-cert -o jsonpath=", Result: testutil.MockResult{Output: []byte("'old-cert-id'")}},
			{Prefix: "get deployment caddy", Result: testutil.MockResult{Err: fmt.Errorf("not found")}},
			{Prefix: "get configmap", Result: testutil.MockResult{Err: fmt.Errorf("not found")}},
			{Prefix: "create namespace", Result: testutil.MockResult{}},
			{Prefix: "get namespace", Result: testutil.MockResult{}},
			{Prefix: "get service web", Result: testutil.MockResult{Output: []byte("3000")}},
			{Prefix: "replace", Result: testutil.MockResult{}},
			{Prefix: "apply", Result: testutil.MockResult{}},
		},
	}

	err := IngressSet(context.Background(), IngressSetRequest{
		Cluster: ingressCluster(out, ssh, mock),
		DNS:     ProviderRef{Name: "cloudflare", Creds: map[string]string{"api_key": "x", "zone_id": "z"}},
		Route:   IngressRouteArg{Service: "web", Domains: []string{"example.com"}, EdgeProxied: true},
	})
	if err != nil {
		t.Fatalf("expected success: %v", err)
	}
	if len(revokedIDs) != 1 || revokedIDs[0] != "old-cert-id" {
		t.Fatalf("expected old cert revoked, got revokedIDs = %v", revokedIDs)
	}
}

// ── Origin CA lifecycle — revocation failure ────────────────────────────────

func TestIngressSet_RevocationFailurePreventsReplacement(t *testing.T) {
	createCalled := false
	origCreate := createOriginCertFunc
	createOriginCertFunc = func(ctx context.Context, apiKey string, domains []string) (*cloudflare.OriginCert, error) {
		createCalled = true
		return nil, fmt.Errorf("should not create")
	}
	defer func() { createOriginCertFunc = origCreate }()

	origRevoke := revokeOriginCertFunc
	revokeOriginCertFunc = func(ctx context.Context, apiKey, certID string) error {
		return fmt.Errorf("cloudflare api down")
	}
	defer func() { revokeOriginCertFunc = origRevoke }()

	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
		GetFirewallRulesFn: func(ctx context.Context, name string) (provider.PortAllowList, error) {
			return provider.PortAllowList{"80": {"173.245.48.0/20"}, "443": {"173.245.48.0/20"}}, nil
		},
	}
	cert := makeTestCert(t, []string{"old.example.com"}, time.Now().Add(24*time.Hour))
	ssh := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "get secret caddy-origin-cert -o jsonpath='{.data.tls.crt}'", Result: testutil.MockResult{Output: []byte("'" + base64.StdEncoding.EncodeToString([]byte(cert)) + "'")}},
			{Prefix: "get secret caddy-origin-cert -o jsonpath='{.data.tls.key}'", Result: testutil.MockResult{Output: []byte("'" + base64.StdEncoding.EncodeToString([]byte("key")) + "'")}},
			{Prefix: "get secret caddy-origin-cert -o jsonpath=", Result: testutil.MockResult{Output: []byte("'old-cert-id'")}},
			{Prefix: "create namespace", Result: testutil.MockResult{}},
			{Prefix: "get namespace", Result: testutil.MockResult{}},
			{Prefix: "get service web", Result: testutil.MockResult{Output: []byte("3000")}},
		},
	}

	err := IngressSet(context.Background(), IngressSetRequest{
		Cluster: ingressCluster(out, ssh, mock),
		DNS:     ProviderRef{Name: "cloudflare", Creds: map[string]string{"api_key": "x", "zone_id": "z"}},
		Route:   IngressRouteArg{Service: "web", Domains: []string{"example.com"}, EdgeProxied: true},
	})
	if err == nil {
		t.Fatal("expected hard error when revocation fails during replacement")
	}
	if !strings.Contains(err.Error(), "revoke Origin CA cert") {
		t.Fatalf("error should mention revocation, got: %v", err)
	}
	if createCalled {
		t.Fatal("should not create new cert when revocation of old cert fails")
	}
}
