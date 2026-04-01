package provider

import "fmt"

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

type computeEntry struct {
	schema  CredentialSchema
	factory func(creds map[string]string) ComputeProvider
}

type buildEntry struct {
	schema  CredentialSchema
	factory func(creds map[string]string) BuildProvider
}

type bucketEntry struct {
	schema  CredentialSchema
	factory func(creds map[string]string) BucketProvider
}

var computeProviders = map[string]computeEntry{}
var buildProviders = map[string]buildEntry{}
var dnsProviders = map[string]func(zone string) (DNSProvider, error){}
var bucketProviders = map[string]bucketEntry{}

func RegisterCompute(name string, schema CredentialSchema, factory func(creds map[string]string) ComputeProvider) {
	computeProviders[name] = computeEntry{schema: schema, factory: factory}
}
func RegisterBuild(name string, schema CredentialSchema, factory func(creds map[string]string) BuildProvider) {
	buildProviders[name] = buildEntry{schema: schema, factory: factory}
}
func RegisterDNS(name string, factory func(string) (DNSProvider, error))   { dnsProviders[name] = factory }
func RegisterBucket(name string, schema CredentialSchema, factory func(creds map[string]string) BucketProvider) {
	bucketProviders[name] = bucketEntry{schema: schema, factory: factory}
}

func GetComputeSchema(name string) (CredentialSchema, error) {
	entry, ok := computeProviders[name]
	if !ok {
		return CredentialSchema{}, fmt.Errorf("unsupported compute provider: %q", name)
	}
	return entry.schema, nil
}

func GetBuildSchema(name string) (CredentialSchema, error) {
	entry, ok := buildProviders[name]
	if !ok {
		return CredentialSchema{}, fmt.Errorf("unsupported build provider: %q", name)
	}
	return entry.schema, nil
}

// ── Resolve ────────────────────────────────────────────────────────────────────

// ResolveCompute creates a compute provider with pre-resolved credentials.
// Credentials must already be fully resolved (flag → env fallback done by caller).
func ResolveCompute(name string, creds map[string]string) (ComputeProvider, error) {
	entry, ok := computeProviders[name]
	if !ok {
		return nil, fmt.Errorf("unsupported compute provider: %q", name)
	}
	if err := entry.schema.Validate(creds); err != nil {
		return nil, err
	}
	return entry.factory(creds), nil
}

func ResolveBuild(name string, creds map[string]string) (BuildProvider, error) {
	entry, ok := buildProviders[name]
	if !ok {
		return nil, fmt.Errorf("unsupported build provider: %q", name)
	}
	if err := entry.schema.Validate(creds); err != nil {
		return nil, err
	}
	return entry.factory(creds), nil
}

func ResolveDNS(name, zone string) (DNSProvider, error) {
	factory, ok := dnsProviders[name]
	if !ok {
		return nil, fmt.Errorf("unsupported DNS provider: %q", name)
	}
	return factory(zone)
}

func GetBucketSchema(name string) (CredentialSchema, error) {
	entry, ok := bucketProviders[name]
	if !ok {
		return CredentialSchema{}, fmt.Errorf("unsupported storage provider: %q", name)
	}
	return entry.schema, nil
}

func ResolveBucket(name string, creds map[string]string) (BucketProvider, error) {
	entry, ok := bucketProviders[name]
	if !ok {
		return nil, fmt.Errorf("unsupported storage provider: %q", name)
	}
	if err := entry.schema.Validate(creds); err != nil {
		return nil, err
	}
	return entry.factory(creds), nil
}

