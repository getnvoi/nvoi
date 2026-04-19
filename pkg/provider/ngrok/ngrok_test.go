package ngrok_test

import (
	"context"
	"testing"

	"github.com/getnvoi/nvoi/internal/testutil"
	"github.com/getnvoi/nvoi/pkg/provider"
)

func TestReconcile_CreatesDomainAndReturnsBindings(t *testing.T) {
	fake := testutil.NewNgrokFake(t)
	fake.Register("ngrok-test")

	tun, err := provider.ResolveTunnel("ngrok-test", map[string]string{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	plan, err := tun.Reconcile(context.Background(), provider.TunnelRequest{
		Name:      "nvoi-myapp-prod",
		Namespace: "nvoi-myapp-prod",
		Labels:    map[string]string{"managed-by": "nvoi"},
		Routes: []provider.TunnelRoute{
			{Hostname: "api.myapp.com", ServiceName: "api", ServicePort: 8080, Scheme: "http"},
		},
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if !fake.Has("create-domain:api.myapp.com") {
		t.Errorf("expected create-domain; ops: %v", fake.All())
	}
	if !fake.Has("list-domains") {
		t.Errorf("expected list-domains; ops: %v", fake.All())
	}

	if _, ok := plan.DNSBindings["api.myapp.com"]; !ok {
		t.Errorf("expected DNS binding for api.myapp.com")
	}
	if plan.DNSBindings["api.myapp.com"].DNSType != "CNAME" {
		t.Errorf("expected CNAME, got %q", plan.DNSBindings["api.myapp.com"].DNSType)
	}

	// Plan has workloads (Deployment + ConfigMap + Secret).
	if len(plan.Workloads) < 3 {
		t.Errorf("expected >=3 workloads, got %d", len(plan.Workloads))
	}
}

func TestReconcile_Idempotent_ExistingDomain(t *testing.T) {
	fake := testutil.NewNgrokFake(t)
	fake.SeedDomain("rd-1", "api.myapp.com", "api.myapp.com.cname.ngrok.io")
	fake.Register("ngrok-idem")

	tun, _ := provider.ResolveTunnel("ngrok-idem", map[string]string{})
	req := provider.TunnelRequest{
		Name:      "nvoi-myapp-prod",
		Namespace: "nvoi-myapp-prod",
		Routes:    []provider.TunnelRoute{{Hostname: "api.myapp.com", ServiceName: "api", ServicePort: 8080}},
	}
	if _, err := tun.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := tun.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("second: %v", err)
	}
	// Create must not be called when domain already exists.
	if fake.Has("create-domain:api.myapp.com") {
		t.Errorf("create-domain must not be called when domain already exists; ops: %v", fake.All())
	}
}

func TestDelete_DeletesDomainsTaggedWithTunnelMetadata(t *testing.T) {
	fake := testutil.NewNgrokFake(t)
	fake.SeedDomainWithMetadata("rd-1", "api.myapp.com", "api.myapp.com.cname.ngrok.io", "managed-by=nvoi;tunnel=nvoi-myapp-prod")
	fake.SeedDomainWithMetadata("rd-2", "www.myapp.com", "www.myapp.com.cname.ngrok.io", "managed-by=nvoi;tunnel=nvoi-myapp-prod")
	fake.SeedDomainWithMetadata("rd-3", "other.myapp.com", "other.myapp.com.cname.ngrok.io", "managed-by=nvoi;tunnel=someone-else")
	fake.Register("ngrok-delete")

	tun, err := provider.ResolveTunnel("ngrok-delete", map[string]string{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if err := tun.Delete(context.Background(), "nvoi-myapp-prod"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if !fake.Has("delete-domain:api.myapp.com") || !fake.Has("delete-domain:www.myapp.com") {
		t.Fatalf("expected both tagged domains deleted; ops: %v", fake.All())
	}
	if fake.Has("delete-domain:other.myapp.com") {
		t.Fatalf("unexpected delete of unrelated domain; ops: %v", fake.All())
	}
}

func TestDelete_DeletesExactHostname(t *testing.T) {
	fake := testutil.NewNgrokFake(t)
	fake.SeedDomain("rd-1", "api.myapp.com", "api.myapp.com.cname.ngrok.io")
	fake.SeedDomain("rd-2", "www.myapp.com", "www.myapp.com.cname.ngrok.io")
	fake.Register("ngrok-delete-host")

	tun, err := provider.ResolveTunnel("ngrok-delete-host", map[string]string{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if err := tun.Delete(context.Background(), "api.myapp.com"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if !fake.Has("delete-domain:api.myapp.com") {
		t.Fatalf("expected api.myapp.com deleted; ops: %v", fake.All())
	}
	if fake.Has("delete-domain:www.myapp.com") {
		t.Fatalf("unexpected delete of www.myapp.com; ops: %v", fake.All())
	}
}
