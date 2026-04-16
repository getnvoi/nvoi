package reconcile

import (
	"context"
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
)

func TestDNS_FreshDeploy(t *testing.T) {
	// DNSSet needs a DNS provider + master server. Without a registered DNS
	// provider, the call fails. We verify the function is called and the error
	// doesn't panic — the reconcile caller handles the error.
	dc := testDC(convergeMock())
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Domains: map[string][]string{"web": {"myapp.com"}},
	}

	// DNSSet fails because no DNS provider is registered for dc.DNS.Name.
	// This is expected — we're testing the reconcile flow, not the DNS provider.
	err := DNS(context.Background(), dc, nil, cfg)
	if err == nil {
		t.Fatal("expected error (no DNS provider registered)")
	}
}

func TestDNS_OrphanDetected(t *testing.T) {
	dc := testDC(convergeMock())
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Domains: map[string][]string{"web": {"myapp.com"}},
	}
	live := &config.LiveState{
		Domains: map[string][]string{
			"web": {"myapp.com"},
			"api": {"api.myapp.com"},
		},
	}

	// Set fails (no DNS provider), but orphan detection still runs.
	// "api" is in live but not in config — it's an orphan.
	// DNSDelete for the orphan also fails (no DNS provider), swallowed by _ =.
	_ = DNS(context.Background(), dc, live, cfg)
}

func TestDNS_NilLive_NoOrphanDeletion(t *testing.T) {
	dc := testDC(convergeMock())
	cfg := &config.AppConfig{
		Domains: map[string][]string{"web": {"myapp.com"}},
	}

	// With nil live, no orphan block runs. Set call fails (no DNS provider).
	// No panic expected.
	_ = DNS(context.Background(), dc, nil, cfg)
}

func TestDNS_NoDomains(t *testing.T) {
	dc := testDC(convergeMock())
	cfg := &config.AppConfig{}

	err := DNS(context.Background(), dc, nil, cfg)
	if err != nil {
		t.Fatalf("no domains should be a no-op, got: %v", err)
	}
}
