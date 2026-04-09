package core

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/internal/testutil"
	"github.com/getnvoi/nvoi/pkg/provider"
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
