package provider

import (
	"context"
	"strings"
	"testing"
)

// stubCompute is a minimal ComputeProvider for testing registration/resolution.
type stubCompute struct{}

func (stubCompute) ValidateCredentials(context.Context) error { return nil }
func (stubCompute) ArchForType(string) string                 { return "amd64" }
func (stubCompute) EnsureServer(context.Context, CreateServerRequest) (*Server, error) {
	return nil, nil
}
func (stubCompute) DeleteServer(context.Context, DeleteServerRequest) error { return nil }
func (stubCompute) ListServers(context.Context, map[string]string) ([]*Server, error) {
	return nil, nil
}
func (stubCompute) DeleteFirewall(context.Context, string) error          { return nil }
func (stubCompute) DeleteNetwork(context.Context, string) error           { return nil }
func (stubCompute) ListAllFirewalls(context.Context) ([]*Firewall, error) { return nil, nil }
func (stubCompute) ListAllNetworks(context.Context) ([]*Network, error)   { return nil, nil }
func (stubCompute) EnsureVolume(context.Context, CreateVolumeRequest) (*Volume, error) {
	return nil, nil
}
func (stubCompute) DetachVolume(context.Context, string) error { return nil }
func (stubCompute) DeleteVolume(context.Context, string) error { return nil }
func (stubCompute) ListVolumes(context.Context, map[string]string) ([]*Volume, error) {
	return nil, nil
}
func (stubCompute) GetPrivateIP(context.Context, string) (string, error)                { return "", nil }
func (stubCompute) ResizeVolume(context.Context, string, int) error                     { return nil }
func (stubCompute) ResolveDevicePath(vol *Volume) string                                { return vol.DevicePath }
func (stubCompute) ListResources(context.Context) ([]ResourceGroup, error)              { return nil, nil }
func (stubCompute) ReconcileFirewallRules(context.Context, string, PortAllowList) error { return nil }
func (stubCompute) GetFirewallRules(context.Context, string) (PortAllowList, error)     { return nil, nil }

// stubSecrets is a minimal SecretsProvider for testing registration/resolution.
type stubSecrets struct{}

func (stubSecrets) ValidateCredentials(context.Context) error   { return nil }
func (stubSecrets) Get(context.Context, string) (string, error) { return "", nil }
func (stubSecrets) Set(context.Context, string, string) error   { return nil }
func (stubSecrets) Delete(context.Context, string) error        { return nil }
func (stubSecrets) List(context.Context) ([]string, error)      { return nil, nil }

func init() {
	RegisterCompute("test-compute", CredentialSchema{
		Name: "test-compute",
		Fields: []CredentialField{
			{Key: "token", Required: true, EnvVar: "TEST_TOKEN", Flag: "token"},
			{Key: "region", Required: false, EnvVar: "TEST_REGION", Flag: "region"},
		},
	}, func(creds map[string]string) ComputeProvider {
		return stubCompute{}
	})

	RegisterSecrets("test-secrets", CredentialSchema{
		Name: "test-secrets",
		Fields: []CredentialField{
			{Key: "token", Required: true, EnvVar: "TEST_SECRETS_TOKEN", Flag: "token"},
			{Key: "project", Required: false, EnvVar: "TEST_SECRETS_PROJECT", Flag: "project"},
		},
	}, func(creds map[string]string) SecretsProvider {
		return stubSecrets{}
	})
}

func TestCredentialSchemaValidate(t *testing.T) {
	schema := CredentialSchema{
		Name: "testprov",
		Fields: []CredentialField{
			{Key: "token", Required: true, EnvVar: "TOKEN", Flag: "token"},
			{Key: "region", Required: false, EnvVar: "REGION", Flag: "region"},
		},
	}

	// All required present.
	err := schema.Validate(map[string]string{"token": "abc"})
	if err != nil {
		t.Errorf("all required present: got error %v, want nil", err)
	}

	// Missing required field.
	err = schema.Validate(map[string]string{"region": "us"})
	if err == nil {
		t.Fatal("missing required: got nil, want error")
	}
	if !strings.Contains(err.Error(), "token") {
		t.Errorf("missing required: error %q should mention field name %q", err, "token")
	}

	// Optional missing is fine.
	err = schema.Validate(map[string]string{"token": "abc"})
	if err != nil {
		t.Errorf("optional missing: got error %v, want nil", err)
	}
}

func TestResolveComputeValid(t *testing.T) {
	p, err := ResolveCompute("test-compute", map[string]string{"token": "abc123"})
	if err != nil {
		t.Fatalf("valid creds: got error %v, want nil", err)
	}
	if p == nil {
		t.Fatal("valid creds: got nil provider")
	}
}

func TestResolveComputeInvalidCreds(t *testing.T) {
	_, err := ResolveCompute("test-compute", map[string]string{})
	if err == nil {
		t.Fatal("invalid creds: got nil, want error")
	}
	if !strings.Contains(err.Error(), "token") {
		t.Errorf("invalid creds: error %q should mention missing field %q", err, "token")
	}
}

func TestMapCredentials(t *testing.T) {
	schema := CredentialSchema{
		Name: "testprov",
		Fields: []CredentialField{
			{Key: "token", EnvVar: "TEST_TOKEN"},
			{Key: "region", EnvVar: "TEST_REGION"},
		},
	}

	// Both present.
	env := map[string]string{"TEST_TOKEN": "abc", "TEST_REGION": "us", "UNRELATED": "xyz"}
	creds := MapCredentials(schema, env)
	if creds["token"] != "abc" {
		t.Errorf("token = %q, want abc", creds["token"])
	}
	if creds["region"] != "us" {
		t.Errorf("region = %q, want us", creds["region"])
	}
	if _, ok := creds["UNRELATED"]; ok {
		t.Error("unrelated key should not be in creds")
	}

	// Missing env var → key absent.
	creds = MapCredentials(schema, map[string]string{"TEST_TOKEN": "abc"})
	if _, ok := creds["region"]; ok {
		t.Error("missing env var should not produce a key")
	}

	// Empty value → key absent.
	creds = MapCredentials(schema, map[string]string{"TEST_TOKEN": ""})
	if _, ok := creds["token"]; ok {
		t.Error("empty value should not produce a key")
	}
}

func TestMapComputeCredentials(t *testing.T) {
	// test-compute registered in init() above.
	creds, err := MapComputeCredentials("test-compute", map[string]string{"TEST_TOKEN": "secret"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds["token"] != "secret" {
		t.Errorf("token = %q, want secret", creds["token"])
	}
}

func TestMapComputeCredentials_UnknownProvider(t *testing.T) {
	_, err := MapComputeCredentials("nope", map[string]string{})
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestResolveComputeUnknownProvider(t *testing.T) {
	_, err := ResolveCompute("no-such-provider", map[string]string{"token": "abc"})
	if err == nil {
		t.Fatal("unknown provider: got nil, want error")
	}
	if !strings.Contains(err.Error(), "no-such-provider") {
		t.Errorf("unknown provider: error %q should mention provider name", err)
	}
}

// ── Secrets provider tests ──────────────────────────────────────────────────

func TestResolveSecretsValid(t *testing.T) {
	p, err := ResolveSecrets("test-secrets", map[string]string{"token": "abc123"})
	if err != nil {
		t.Fatalf("valid creds: got error %v, want nil", err)
	}
	if p == nil {
		t.Fatal("valid creds: got nil provider")
	}
}

func TestResolveSecretsInvalidCreds(t *testing.T) {
	_, err := ResolveSecrets("test-secrets", map[string]string{})
	if err == nil {
		t.Fatal("invalid creds: got nil, want error")
	}
	if !strings.Contains(err.Error(), "token") {
		t.Errorf("invalid creds: error %q should mention missing field %q", err, "token")
	}
}

func TestResolveSecretsUnknownProvider(t *testing.T) {
	_, err := ResolveSecrets("no-such-provider", map[string]string{"token": "abc"})
	if err == nil {
		t.Fatal("unknown provider: got nil, want error")
	}
	if !strings.Contains(err.Error(), "no-such-provider") {
		t.Errorf("unknown provider: error %q should mention provider name", err)
	}
}

func TestGetSecretsSchema(t *testing.T) {
	schema, err := GetSecretsSchema("test-secrets")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if schema.Name != "test-secrets" {
		t.Errorf("schema.Name = %q, want test-secrets", schema.Name)
	}
}

func TestGetSecretsSchema_Unknown(t *testing.T) {
	_, err := GetSecretsSchema("nope")
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestMapSecretsCredentials(t *testing.T) {
	creds, err := MapSecretsCredentials("test-secrets", map[string]string{"TEST_SECRETS_TOKEN": "secret"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds["token"] != "secret" {
		t.Errorf("token = %q, want secret", creds["token"])
	}
}

func TestMapSecretsCredentials_UnknownProvider(t *testing.T) {
	_, err := MapSecretsCredentials("nope", map[string]string{})
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestGetSchema_Secrets(t *testing.T) {
	schema, err := GetSchema("secrets", "test-secrets")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if schema.Name != "test-secrets" {
		t.Errorf("schema.Name = %q, want test-secrets", schema.Name)
	}
}
