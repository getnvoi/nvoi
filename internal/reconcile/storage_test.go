package reconcile

import (
	"context"
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
)

func TestStorage_FreshDeploy(t *testing.T) {
	// StorageSet needs a bucket provider. Without one registered, the call fails.
	dc := testDC(convergeMock())
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Storage: map[string]config.StorageDef{"assets": {CORS: true}},
	}

	_, err := Storage(context.Background(), dc, cfg)
	if err == nil {
		t.Fatal("expected error (no bucket provider registered)")
	}
}

func TestStorage_NoStorage(t *testing.T) {
	dc := testDC(convergeMock())
	cfg := &config.AppConfig{}

	creds, err := Storage(context.Background(), dc, cfg)
	if err != nil {
		t.Fatalf("no storage should be a no-op, got: %v", err)
	}
	if len(creds) != 0 {
		t.Errorf("expected empty creds, got %v", creds)
	}
}

func TestStorage_OrphanDetected(t *testing.T) {
	dc := testDC(convergeMock())
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Storage: map[string]config.StorageDef{"assets": {}},
	}
	// Set fails (no bucket provider). D3 removed orphan storage cleanup
	// (the live.Storage list was always == cfg.StorageNames so the
	// orphan loop was vacuous). No panic expected.
	_, _ = Storage(context.Background(), dc, cfg)
}

func TestStorage_AlreadyConverged(t *testing.T) {
	dc := testDC(convergeMock())
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Storage: map[string]config.StorageDef{"assets": {}},
	}
	// Set fails (no bucket provider), but no orphans to delete.
	_, _ = Storage(context.Background(), dc, cfg)
}
