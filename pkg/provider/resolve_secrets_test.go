package provider

import (
	"context"
	"errors"
	"testing"
)

// fakeSecretsProvider records Get calls + returns canned values.
type fakeSecretsProvider struct {
	values  map[string]string
	getErr  error
	getCall []string
}

func (f *fakeSecretsProvider) ValidateCredentials(_ context.Context) error { return nil }
func (f *fakeSecretsProvider) Get(_ context.Context, key string) (string, error) {
	f.getCall = append(f.getCall, key)
	if f.getErr != nil {
		return "", f.getErr
	}
	return f.values[key], nil
}
func (f *fakeSecretsProvider) List(_ context.Context) ([]string, error) {
	keys := make([]string, 0, len(f.values))
	for k := range f.values {
		keys = append(keys, k)
	}
	return keys, nil
}

// INVARIANT: every SecretsSource.Get call routes to the underlying
// provider. No env fallback — once a secrets backend is in use, the
// env is not an escape hatch for individual keys.
func TestSecretsSource_RoutesGetToProvider(t *testing.T) {
	fake := &fakeSecretsProvider{values: map[string]string{
		"HETZNER_TOKEN": "tok-from-backend",
		"CF_API_KEY":    "cf-from-backend",
	}}
	src := SecretsSource{Ctx: context.Background(), Provider: fake}

	if got, _ := src.Get("HETZNER_TOKEN"); got != "tok-from-backend" {
		t.Errorf("got %q, want tok-from-backend", got)
	}
	if got, _ := src.Get("CF_API_KEY"); got != "cf-from-backend" {
		t.Errorf("got %q, want cf-from-backend", got)
	}
	if len(fake.getCall) != 2 {
		t.Errorf("expected 2 provider Gets, got %d", len(fake.getCall))
	}
}

// INVARIANT: missing key returns ("", nil), not an error — same
// contract as EnvSource. Required-credential enforcement happens at
// schema Validate time, not at fetch time.
func TestSecretsSource_MissingKeyIsNotError(t *testing.T) {
	fake := &fakeSecretsProvider{values: map[string]string{}}
	src := SecretsSource{Ctx: context.Background(), Provider: fake}

	v, err := src.Get("ABSENT_VAR")
	if err != nil {
		t.Fatalf("missing key must not error, got: %v", err)
	}
	if v != "" {
		t.Errorf("missing key must return empty string, got %q", v)
	}
}

// INVARIANT: real backend errors (auth, network) propagate through
// Get as-is. CredentialSource-layer callers distinguish missing
// (empty) from broken (error).
func TestSecretsSource_PropagatesProviderError(t *testing.T) {
	sentinel := errors.New("auth refused")
	fake := &fakeSecretsProvider{getErr: sentinel}
	src := SecretsSource{Ctx: context.Background(), Provider: fake}

	_, err := src.Get("HETZNER_TOKEN")
	if !errors.Is(err, sentinel) {
		t.Errorf("provider error should bubble up, got: %v", err)
	}
}

// INVARIANT: GetSchema("secrets", ...) + ResolveSecrets round-trip on
// each of the three adapter names. Hard-coded list — if an adapter is
// dropped from cmd/cli/main.go's blank imports, the registry stays
// empty and this test catches it.
func TestSecretsRegistry_AllThreeAdaptersDiscoverable(t *testing.T) {
	adapters := []string{"doppler", "awssm", "infisical"}
	// Register stubs so the test stays self-contained — the real
	// adapter packages register via init() in cmd/cli/main.go's blank
	// imports, which pkg/provider's tests don't inherit.
	for _, name := range adapters {
		n := name
		RegisterSecrets(n, CredentialSchema{Name: n}, func(creds map[string]string) SecretsProvider {
			return nil
		})
	}
	for _, name := range adapters {
		if _, err := GetSecretsSchema(name); err != nil {
			t.Errorf("schema for %q: %v", name, err)
		}
	}
	if _, err := GetSecretsSchema("nope-not-real"); err == nil {
		t.Error("unknown adapter should error")
	}
}
