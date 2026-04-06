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
type dnsEntry struct {
	schema  CredentialSchema
	factory func(creds map[string]string) DNSProvider
}

var dnsProviders = map[string]dnsEntry{}
var bucketProviders = map[string]bucketEntry{}

func RegisterCompute(name string, schema CredentialSchema, factory func(creds map[string]string) ComputeProvider) {
	computeProviders[name] = computeEntry{schema: schema, factory: factory}
}
func RegisterBuild(name string, schema CredentialSchema, factory func(creds map[string]string) BuildProvider) {
	buildProviders[name] = buildEntry{schema: schema, factory: factory}
}
func RegisterDNS(name string, schema CredentialSchema, factory func(creds map[string]string) DNSProvider) {
	dnsProviders[name] = dnsEntry{schema: schema, factory: factory}
}
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

// ── Credential mapping ────────────────────────────────────────────────────────

// MapCredentials translates a raw env map (e.g. {"HETZNER_TOKEN": "xxx"}) into
// schema-keyed credentials (e.g. {"token": "xxx"}) using the schema's EnvVar mappings.
// Both the direct CLI and the API executor use this as the single source of truth
// for env-var-to-key translation.
func MapCredentials(schema CredentialSchema, env map[string]string) map[string]string {
	creds := make(map[string]string, len(schema.Fields))
	for _, f := range schema.Fields {
		if v, ok := env[f.EnvVar]; ok && v != "" {
			creds[f.Key] = v
		}
	}
	return creds
}

// MapComputeCredentials is a convenience wrapper: looks up the schema by provider name,
// then maps credentials. Returns an error if the provider is not registered.
func MapComputeCredentials(providerName string, env map[string]string) (map[string]string, error) {
	schema, err := GetComputeSchema(providerName)
	if err != nil {
		return nil, err
	}
	return MapCredentials(schema, env), nil
}

// MapDNSCredentials maps env vars to DNS provider schema keys.
func MapDNSCredentials(providerName string, env map[string]string) (map[string]string, error) {
	schema, err := GetDNSSchema(providerName)
	if err != nil {
		return nil, err
	}
	return MapCredentials(schema, env), nil
}

// MapBucketCredentials maps env vars to bucket provider schema keys.
func MapBucketCredentials(providerName string, env map[string]string) (map[string]string, error) {
	schema, err := GetBucketSchema(providerName)
	if err != nil {
		return nil, err
	}
	return MapCredentials(schema, env), nil
}

// MapBuildCredentials maps env vars to build provider schema keys.
func MapBuildCredentials(providerName string, env map[string]string) (map[string]string, error) {
	schema, err := GetBuildSchema(providerName)
	if err != nil {
		return nil, err
	}
	return MapCredentials(schema, env), nil
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

func GetDNSSchema(name string) (CredentialSchema, error) {
	entry, ok := dnsProviders[name]
	if !ok {
		return CredentialSchema{}, fmt.Errorf("unsupported DNS provider: %q", name)
	}
	return entry.schema, nil
}

func ResolveDNS(name string, creds map[string]string) (DNSProvider, error) {
	entry, ok := dnsProviders[name]
	if !ok {
		return nil, fmt.Errorf("unsupported DNS provider: %q", name)
	}
	if err := entry.schema.Validate(creds); err != nil {
		return nil, err
	}
	return entry.factory(creds), nil
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

