package local

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/pkg/provider"
)

// TestLocalBuilder_RegistersOnImport verifies the init() wiring: merely
// importing the package must make "local" addressable via the registry,
// without any explicit setup. cmd/cli/main.go and reconcile's test binary
// both rely on this — a blank import is the only activation step.
func TestLocalBuilder_RegistersOnImport(t *testing.T) {
	if _, err := provider.GetBuildSchema("local"); err != nil {
		t.Fatalf("local build provider not registered: %v", err)
	}
	caps, err := provider.GetBuildCapability("local")
	if err != nil {
		t.Fatalf("GetBuildCapability(local): %v", err)
	}
	// Local is the in-process substrate — no remote builder, not a CI
	// target. Locking the bits here because the validator (R1) and PR-C's
	// ci validator (R2) both depend on these exact values.
	if caps.RequiresBuilders {
		t.Errorf("local.RequiresBuilders: got true, want false (local runs in-process)")
	}
	if caps.DispatchableFromCI {
		t.Errorf("local.DispatchableFromCI: got true, want false (CI needs a remote substrate)")
	}
}

// TestLocalBuilder_ResolveReturnsInstance verifies ResolveBuild round-trips
// through the factory. Local takes no credentials, so an empty map must
// succeed and the returned provider must satisfy BuildProvider.
func TestLocalBuilder_ResolveReturnsInstance(t *testing.T) {
	b, err := provider.ResolveBuild("local", nil)
	if err != nil {
		t.Fatalf("ResolveBuild(local): %v", err)
	}
	if b == nil {
		t.Fatal("ResolveBuild(local) returned nil provider")
	}
	if err := b.Close(); err != nil {
		t.Errorf("Close: got %v, want nil", err)
	}
}

// TestLocalBuilder_DispatchIsSafetyNet locks the contract that cmd/cli/deploy.go
// must short-circuit to reconcile.Deploy for the "local" provider — calling
// Dispatch means the CLI wiring regressed, and the error message must point
// at the correct fix site.
func TestLocalBuilder_DispatchIsSafetyNet(t *testing.T) {
	b, err := provider.ResolveBuild("local", nil)
	if err != nil {
		t.Fatalf("ResolveBuild(local): %v", err)
	}
	defer b.Close()

	err = b.Dispatch(context.Background(), provider.BuildDispatch{})
	if err == nil {
		t.Fatal("Dispatch on local must error — got nil")
	}
	if !errors.Is(err, errDispatchCalled) {
		t.Errorf("Dispatch error is not errDispatchCalled: got %v", err)
	}
	// The message must name the fix site so a regressed CLI doesn't leave
	// the operator guessing. Locking the substring here cheap-guards the
	// doc contract.
	if !strings.Contains(err.Error(), "cmd/cli/deploy.go") {
		t.Errorf("Dispatch error must reference cmd/cli/deploy.go, got: %v", err)
	}
}
