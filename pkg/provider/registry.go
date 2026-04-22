package provider

import "fmt"

// registry is the shared generic backing store for every provider-kind
// registry (infra, dns, storage, secrets, tunnel, build, ci). Each kind
// instantiates one *registry[T] at package scope and exposes kind-named
// thin wrappers (RegisterInfra / ResolveInfra / …) over it.
//
// Why this exists: before this consolidation every provider kind carried
// its own near-identical Register + Resolve + GetSchema trio with its own
// entry struct and its own mutex-free map. Seven copies of the same
// shape drifted (infra.go silently diverged from resolve.go in small
// wording) and cost ~150 LOC of boilerplate. One generic, one definition
// of "what a provider registry does" — all seven behave identically by
// construction.
//
// kindDisplay is the exact token used in error messages ("infra", "DNS",
// "storage", "secrets", "tunnel", "build", "CI"). Not all kinds match
// their interface name — "storage" surfaces `BucketProvider`, matching
// the `providers.storage:` YAML key the operator sees. Keep the strings
// stable: internal/reconcile/validate_test.go asserts substrings
// ("unsupported build provider") and external docs reference them.
//
// Unexported on purpose. The registry is an implementation detail —
// every caller goes through the kind-specific RegisterX / ResolveX /
// GetXSchema wrappers. No caller constructs or references a registry[T]
// directly outside this package.
type registry[T any] struct {
	kindDisplay string
	entries     map[string]registryEntry[T]
}

type registryEntry[T any] struct {
	schema  CredentialSchema
	factory func(creds map[string]string) T
}

// newRegistry creates an empty registry for a provider kind. `kindDisplay`
// is the token embedded in "unsupported %s provider" error messages.
func newRegistry[T any](kindDisplay string) *registry[T] {
	return &registry[T]{
		kindDisplay: kindDisplay,
		entries:     map[string]registryEntry[T]{},
	}
}

// register installs (or replaces) an entry. Re-registration replaces —
// matches the prior per-kind behavior; tests that swap a real provider
// for a stub depend on it.
func (r *registry[T]) register(name string, schema CredentialSchema, factory func(creds map[string]string) T) {
	r.entries[name] = registryEntry[T]{schema: schema, factory: factory}
}

// getSchema returns the credential schema for a registered provider name.
// Error shape — "unsupported <kind> provider: %q" — is load-bearing: the
// validator surface-tests on it (internal/reconcile/validate.go).
func (r *registry[T]) getSchema(name string) (CredentialSchema, error) {
	e, ok := r.entries[name]
	if !ok {
		var zero CredentialSchema
		return zero, fmt.Errorf("unsupported %s provider: %q", r.kindDisplay, name)
	}
	return e.schema, nil
}

// resolve validates the caller-supplied credentials against the schema
// and returns a live provider instance from the factory. Credentials
// must be pre-resolved (source → map). Missing-required → error before
// the factory ever runs; the factory is called exactly once per resolve.
func (r *registry[T]) resolve(name string, creds map[string]string) (T, error) {
	var zero T
	e, ok := r.entries[name]
	if !ok {
		return zero, fmt.Errorf("unsupported %s provider: %q", r.kindDisplay, name)
	}
	if err := e.schema.Validate(creds); err != nil {
		return zero, err
	}
	return e.factory(creds), nil
}
