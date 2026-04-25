package provider

import (
	"strings"
	"testing"
)

// stubTarget is the minimal target type for generic registry tests.
// Tests construct values of this type through the factory — the point
// is exercising the registry plumbing, not a real provider.
type stubTarget struct {
	creds map[string]string
}

func schemaRequiringToken() CredentialSchema {
	return CredentialSchema{
		Name: "stub",
		Fields: []CredentialField{
			{Key: "token", Required: true, EnvVar: "STUB_TOKEN", Flag: "token"},
		},
	}
}

func TestRegistry_ResolveValid(t *testing.T) {
	r := newRegistry[stubTarget]("stub")
	r.register("primary", schemaRequiringToken(), func(creds map[string]string) stubTarget {
		return stubTarget{creds: creds}
	})

	got, err := r.resolve("primary", map[string]string{"token": "abc"})
	if err != nil {
		t.Fatalf("valid creds: got error %v, want nil", err)
	}
	if got.creds["token"] != "abc" {
		t.Errorf("factory received creds %v, want token=abc", got.creds)
	}
}

func TestRegistry_ResolveUnknownName(t *testing.T) {
	r := newRegistry[stubTarget]("stub")
	r.register("primary", schemaRequiringToken(), func(creds map[string]string) stubTarget {
		return stubTarget{}
	})

	_, err := r.resolve("nope", map[string]string{"token": "abc"})
	if err == nil {
		t.Fatal("unknown name: got nil, want error")
	}
	// Error shape is load-bearing — the validator (internal/reconcile/validate.go)
	// and callers like registration_test.go substring-match on "unsupported".
	if !strings.Contains(err.Error(), "unsupported stub provider") {
		t.Errorf("error %q should embed kind display string", err)
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("error %q should embed provider name", err)
	}
}

func TestRegistry_ResolveMissingRequiredCred(t *testing.T) {
	r := newRegistry[stubTarget]("stub")
	factoryCalled := false
	r.register("primary", schemaRequiringToken(), func(creds map[string]string) stubTarget {
		factoryCalled = true
		return stubTarget{}
	})

	_, err := r.resolve("primary", map[string]string{})
	if err == nil {
		t.Fatal("missing required: got nil, want error")
	}
	if factoryCalled {
		t.Error("factory must not run when schema validation fails")
	}
	if !strings.Contains(err.Error(), "token") {
		t.Errorf("error %q should mention missing field", err)
	}
}

func TestRegistry_GetSchemaUnknownName(t *testing.T) {
	r := newRegistry[stubTarget]("stub")
	_, err := r.getSchema("nope")
	if err == nil {
		t.Fatal("unknown name: got nil, want error")
	}
	if !strings.Contains(err.Error(), "unsupported stub provider") {
		t.Errorf("error %q should embed kind display string", err)
	}
}

func TestRegistry_RegisterReplaces(t *testing.T) {
	r := newRegistry[stubTarget]("stub")
	r.register("primary", schemaRequiringToken(), func(creds map[string]string) stubTarget {
		return stubTarget{creds: map[string]string{"version": "v1"}}
	})
	r.register("primary", schemaRequiringToken(), func(creds map[string]string) stubTarget {
		return stubTarget{creds: map[string]string{"version": "v2"}}
	})

	got, err := r.resolve("primary", map[string]string{"token": "abc"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.creds["version"] != "v2" {
		t.Errorf("got version %q, want v2 (re-registration should replace)", got.creds["version"])
	}
}

func TestRegistry_FactoryCalledExactlyOnce(t *testing.T) {
	r := newRegistry[stubTarget]("stub")
	calls := 0
	r.register("primary", schemaRequiringToken(), func(creds map[string]string) stubTarget {
		calls++
		return stubTarget{}
	})

	if _, err := r.resolve("primary", map[string]string{"token": "abc"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Errorf("factory called %d times, want 1", calls)
	}
}
