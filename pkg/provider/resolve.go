package provider

import (
	"context"
	"fmt"
	"os"
)

// ── Credential source ─────────────────────────────────────────────────────────

// CredentialSource abstracts where a credential value comes from.
// The single resolution layer — callers never branch on source type.
type CredentialSource interface {
	// Get returns the value for a credential field's env var key.
	// Returns ("", nil) if the key is absent — not an error.
	Get(key string) (string, error)
}

// EnvSource reads credentials from os.Getenv. Default source at the cmd/ boundary.
type EnvSource struct{}

func (EnvSource) Get(key string) (string, error) {
	return os.Getenv(key), nil
}

// MapSource reads credentials from an in-memory map. Used by tests.
type MapSource struct {
	M map[string]string
}

func (s MapSource) Get(key string) (string, error) {
	return s.M[key], nil
}

// SecretsSource reads credentials from a SecretsProvider (Doppler, AWS
// Secrets Manager, Infisical, …). Used when providers.secrets is
// configured in nvoi.yaml — every credential the deploy touches
// (compute / DNS / storage / SSH key / service $VAR expansion) is
// fetched through the provider at deploy time. No disk fallback, no
// env fallback — the secrets provider is THE source.
//
// The embedded ctx lets callers use the same cancellation scope as the
// rest of the deploy without plumbing ctx through every Get() call.
type SecretsSource struct {
	Ctx      context.Context
	Provider SecretsProvider
}

func (s SecretsSource) Get(key string) (string, error) {
	return s.Provider.Get(s.Ctx, key)
}

// ResolveFrom resolves credentials for a provider schema from any source.
// Iterates schema fields, calls source.Get(field.EnvVar) for each.
// Returns schema-keyed map (e.g. {"token": "xxx"}).
func ResolveFrom(schema CredentialSchema, source CredentialSource) (map[string]string, error) {
	creds := make(map[string]string, len(schema.Fields))
	for _, f := range schema.Fields {
		v, err := source.Get(f.EnvVar)
		if err != nil {
			return nil, fmt.Errorf("%s: fetch %s: %w", schema.Name, f.Key, err)
		}
		if v != "" {
			creds[f.Key] = v
		}
	}
	return creds, nil
}

// ── Credential schema ──────────────────────────────────────────────────────────

// CredentialField describes one credential a provider needs.
type CredentialField struct {
	Key      string // internal key (e.g. "token", "access_key_id")
	Required bool
	EnvVar   string // env var convention (e.g. "HETZNER_TOKEN") — for documentation/cmd layer
	Flag     string // CLI flag name (e.g. "token") — for documentation/cmd layer
}

// CredentialSchema describes all credentials a provider needs.
type CredentialSchema struct {
	Name   string
	Fields []CredentialField
}

// Validate checks that all required credentials are present.
// No env var lookup — caller must have already resolved everything.
func (s CredentialSchema) Validate(creds map[string]string) error {
	for _, f := range s.Fields {
		if f.Required {
			if v, ok := creds[f.Key]; !ok || v == "" {
				return fmt.Errorf("%s: %s is required (flag: --%s, env: %s)", s.Name, f.Key, f.Flag, f.EnvVar)
			}
		}
	}
	return nil
}

// ── Registries ─────────────────────────────────────────────────────────────────
//
// ComputeProvider / RegisterCompute / ResolveCompute were deleted in C10
// (refactor #47). InfraProvider replaces ComputeProvider; the registry
// for it lives in infra.go. Every provider-kind registry — including the
// three in this file — is a thin facade over the generic backing store
// in registry.go.

var (
	dnsRegistry     = newRegistry[DNSProvider]("DNS")
	bucketRegistry  = newRegistry[BucketProvider]("storage")
	secretsRegistry = newRegistry[SecretsProvider]("secrets")
)

func RegisterDNS(name string, schema CredentialSchema, factory func(creds map[string]string) DNSProvider) {
	dnsRegistry.register(name, schema, factory)
}
func RegisterBucket(name string, schema CredentialSchema, factory func(creds map[string]string) BucketProvider) {
	bucketRegistry.register(name, schema, factory)
}
func RegisterSecrets(name string, schema CredentialSchema, factory func(creds map[string]string) SecretsProvider) {
	secretsRegistry.register(name, schema, factory)
}

// GetSchema returns the credential schema for any provider kind + name.
// The cross-kind dispatcher — used by cmd/cli/context.go and cmd/cli/ci.go
// to enumerate every configured provider's schema from a YAML kind string.
// Go generics can't dispatch on a runtime string, so this stays a switch.
func GetSchema(kind, name string) (CredentialSchema, error) {
	switch kind {
	case "infra":
		return GetInfraSchema(name)
	case "dns":
		return GetDNSSchema(name)
	case "storage":
		return GetBucketSchema(name)
	case "secrets":
		return GetSecretsSchema(name)
	case "tunnel":
		return GetTunnelSchema(name)
	case "build":
		return GetBuildSchema(name)
	case "ci":
		return GetCISchema(name)
	default:
		return CredentialSchema{}, fmt.Errorf("unknown provider kind %q", kind)
	}
}

func GetDNSSchema(name string) (CredentialSchema, error) {
	return dnsRegistry.getSchema(name)
}

func ResolveDNS(name string, creds map[string]string) (DNSProvider, error) {
	return dnsRegistry.resolve(name, creds)
}

func GetBucketSchema(name string) (CredentialSchema, error) {
	return bucketRegistry.getSchema(name)
}

func ResolveBucket(name string, creds map[string]string) (BucketProvider, error) {
	return bucketRegistry.resolve(name, creds)
}

func GetSecretsSchema(name string) (CredentialSchema, error) {
	return secretsRegistry.getSchema(name)
}

// ResolveSecrets creates a secrets provider with pre-resolved credentials.
// Same contract as the other Resolve* functions — credentials must be
// validated before factory construction so a misconfigured provider
// fails loudly at startup instead of deferring the error mid-deploy.
func ResolveSecrets(name string, creds map[string]string) (SecretsProvider, error) {
	return secretsRegistry.resolve(name, creds)
}
