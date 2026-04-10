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

	err := Storage(context.Background(), dc, nil, cfg)
	if err == nil {
		t.Fatal("expected error (no bucket provider registered)")
	}
}

func TestStorage_NoStorage(t *testing.T) {
	dc := testDC(convergeMock())
	cfg := &config.AppConfig{}

	err := Storage(context.Background(), dc, nil, cfg)
	if err != nil {
		t.Fatalf("no storage should be a no-op, got: %v", err)
	}
}

func TestStorage_OrphanDetected(t *testing.T) {
	dc := testDC(convergeMock())
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Storage: map[string]config.StorageDef{"assets": {}},
	}
	live := &config.LiveState{Storage: []string{"assets", "old-uploads"}}

	// Set fails (no bucket provider). Orphan "old-uploads" detected, delete also fails.
	// No panic expected.
	_ = Storage(context.Background(), dc, live, cfg)
}

func TestStorage_AlreadyConverged(t *testing.T) {
	dc := testDC(convergeMock())
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Storage: map[string]config.StorageDef{"assets": {}},
	}
	live := &config.LiveState{Storage: []string{"assets"}}

	// Set fails (no bucket provider), but no orphans to delete.
	_ = Storage(context.Background(), dc, live, cfg)
}
