package core

import (
	"context"
	"fmt"
	"testing"

	"github.com/getnvoi/nvoi/internal/testutil"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// dnsDeleteCluster wires a Hetzner fake (for compute lookups) and returns a
// Cluster targeting it.
func dnsDeleteCluster(t *testing.T, out *testutil.MockOutput, ssh utils.SSHClient) (*testutil.HetznerFake, Cluster) {
	hz := testutil.NewHetznerFake(t)
	hz.SeedServer("nvoi-test-prod-master", "1.2.3.4", "10.0.1.1")
	provName := fmt.Sprintf("dns-delete-test-%p", hz)
	hz.Register(provName)
	cl := Cluster{
		AppName: "test", Env: "prod",
		Provider: provName, Output: out,
		SSHFunc: func(ctx context.Context, addr string) (utils.SSHClient, error) { return ssh, nil },
	}
	return hz, cl
}

func TestDNSDelete_DeletesRecords(t *testing.T) {
	out := &testutil.MockOutput{}
	ssh := &testutil.MockSSH{}
	_, cl := dnsDeleteCluster(t, out, ssh)

	cf := testutil.NewCloudflareFake(t, testutil.CloudflareFakeOptions{
		ZoneID:     "Z1",
		ZoneDomain: "example.com",
	})
	provName := fmt.Sprintf("dns-test-%p", cf)
	cf.RegisterDNS(provName)
	cf.SeedDNSRecord("example.com", "1.2.3.4", "A")
	cf.SeedDNSRecord("www.example.com", "1.2.3.4", "A")

	err := DNSDelete(context.Background(), DNSDeleteRequest{
		Cluster: cl,
		DNS:     ProviderRef{Name: provName},
		Service: "web",
		Domains: []string{"example.com", "www.example.com"},
	})

	if err != nil {
		t.Fatalf("dns delete should succeed: %v", err)
	}
	if !cf.Has("delete-dns:example.com") {
		t.Errorf("example.com not deleted: %v", cf.All())
	}
	if !cf.Has("delete-dns:www.example.com") {
		t.Errorf("www.example.com not deleted: %v", cf.All())
	}
	if cf.Count("delete-dns:") != 2 {
		t.Errorf("expected 2 deletes, got %d: %v", cf.Count("delete-dns:"), cf.All())
	}
}
