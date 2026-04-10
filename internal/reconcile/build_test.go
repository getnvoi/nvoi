package reconcile

import (
	"context"
	"testing"
)

func TestBuild_Empty(t *testing.T) {
	dc := testDC(convergeMock())
	cfg := &AppConfig{}

	err := Build(context.Background(), dc, cfg)
	if err != nil {
		t.Fatalf("empty build should be a no-op, got: %v", err)
	}
}

func TestBuild_NoBuildProvider(t *testing.T) {
	dc := testDC(convergeMock())
	cfg := &AppConfig{
		Build: map[string]string{"web": "org/repo"},
	}

	// No build provider registered on dc.Builder — fails.
	err := Build(context.Background(), dc, cfg)
	if err == nil {
		t.Fatal("expected error (no build provider)")
	}
}
