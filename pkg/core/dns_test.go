package core

import (
	"context"
	"fmt"
	"testing"

	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/testutil"
)

func dnsDeleteCluster(mock *testutil.MockCompute) Cluster {
	provName := fmt.Sprintf("dns-delete-test-%p", mock)
	provider.RegisterCompute(provName, provider.CredentialSchema{Name: provName}, func(creds map[string]string) provider.ComputeProvider {
		return mock
	})
	return Cluster{
		AppName: "test", Env: "prod",
		Provider: provName,
		MasterIP: "1.2.3.4",
	}
}

func registerMockDNS(mock *testutil.MockDNS) string {
	name := fmt.Sprintf("dns-mock-%p", mock)
	provider.RegisterDNS(name, provider.CredentialSchema{Name: name}, func(creds map[string]string) provider.DNSProvider {
		return mock
	})
	return name
}

func TestDNSDelete_DeletesRecords(t *testing.T) {
	out := &testutil.MockOutput{}
	mockDNS := &testutil.MockDNS{}
	mockCompute := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
	}

	err := DNSDelete(context.Background(), DNSDeleteRequest{
		Cluster: dnsDeleteCluster(mockCompute), Output: out,
		DNS:     ProviderRef{Name: registerMockDNS(mockDNS)},
		Service: "web",
		Domains: []string{"example.com", "www.example.com"},
	})

	if err != nil {
		t.Fatalf("dns delete should succeed: %v", err)
	}
	if len(mockDNS.DeletedA) != 2 {
		t.Fatalf("deleted records = %v, want 2 domains", mockDNS.DeletedA)
	}
}
