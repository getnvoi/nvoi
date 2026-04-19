package cloudflare_test

import (
	"context"
	"testing"

	"github.com/getnvoi/nvoi/internal/testutil"
	"github.com/getnvoi/nvoi/pkg/provider"
)

func TestReconcile_CreatesTunnelAndPushesConfig(t *testing.T) {
	fake := testutil.NewCloudflareTunnelFake(t)
	fake.Register("cf-test")

	tun, err := provider.ResolveTunnel("cf-test", map[string]string{})
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

	// Tunnel was created.
	if !fake.Has("create-tunnel:nvoi-myapp-prod") {
		t.Errorf("expected create-tunnel; ops: %v", fake.All())
	}
	// Config was pushed.
	if fake.Count("config:") != 1 {
		t.Errorf("expected 1 config push; got %d", fake.Count("config:"))
	}

	// Plan has workloads (Deployment + Secret).
	if len(plan.Workloads) < 2 {
		t.Errorf("expected >=2 workloads, got %d", len(plan.Workloads))
	}

	// DNS binding for the hostname.
	if _, ok := plan.DNSBindings["api.myapp.com"]; !ok {
		t.Errorf("expected DNS binding for api.myapp.com; bindings: %v", plan.DNSBindings)
	}
	if plan.DNSBindings["api.myapp.com"].DNSType != "CNAME" {
		t.Errorf("expected CNAME type, got %q", plan.DNSBindings["api.myapp.com"].DNSType)
	}
}

func TestReconcile_Idempotent_ExistingTunnel(t *testing.T) {
	fake := testutil.NewCloudflareTunnelFake(t)
	fake.SeedTunnel("existing-id", "nvoi-myapp-prod", "tok-existing")
	fake.Register("cf-idempotent")

	tun, _ := provider.ResolveTunnel("cf-idempotent", map[string]string{})
	req := provider.TunnelRequest{
		Name:      "nvoi-myapp-prod",
		Namespace: "nvoi-myapp-prod",
		Routes:    []provider.TunnelRoute{{Hostname: "api.myapp.com", ServiceName: "api", ServicePort: 8080}},
	}

	if _, err := tun.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("first Reconcile: %v", err)
	}
	if _, err := tun.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}

	// Create must NOT have been called — tunnel already existed.
	if fake.Has("create-tunnel:nvoi-myapp-prod") {
		t.Errorf("create-tunnel must not be called when tunnel already exists; ops: %v", fake.All())
	}
}

func TestDelete_RemovesTunnel(t *testing.T) {
	fake := testutil.NewCloudflareTunnelFake(t)
	fake.SeedTunnel("del-id", "nvoi-myapp-prod", "tok-del")
	fake.Register("cf-delete")

	tun, _ := provider.ResolveTunnel("cf-delete", map[string]string{})
	if err := tun.Delete(context.Background(), "nvoi-myapp-prod"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if !fake.Has("delete-tunnel:del-id") {
		t.Errorf("expected delete-tunnel; ops: %v", fake.All())
	}
}

func TestDelete_Idempotent_AlreadyGone(t *testing.T) {
	fake := testutil.NewCloudflareTunnelFake(t)
	fake.Register("cf-delete-idem")

	tun, _ := provider.ResolveTunnel("cf-delete-idem", map[string]string{})
	if err := tun.Delete(context.Background(), "nvoi-myapp-prod"); err != nil {
		t.Fatalf("Delete on absent tunnel should succeed: %v", err)
	}
}

func TestLookup_RequiresIsDeletedFalse(t *testing.T) {
	// This test verifies the is_deleted=false invariant is enforced at the
	// fake level — any lookup that omits it gets a 400.
	fake := testutil.NewCloudflareTunnelFake(t)
	fake.Register("cf-invariant")

	tun, _ := provider.ResolveTunnel("cf-invariant", map[string]string{})
	// Normal Reconcile must succeed (client sends is_deleted=false).
	_, err := tun.Reconcile(context.Background(), provider.TunnelRequest{
		Name:      "nvoi-myapp-prod",
		Namespace: "nvoi-myapp-prod",
		Routes:    []provider.TunnelRoute{{Hostname: "api.myapp.com", ServiceName: "api", ServicePort: 8080}},
	})
	if err != nil {
		t.Fatalf("Reconcile failed — client likely omitted is_deleted=false: %v", err)
	}
}
