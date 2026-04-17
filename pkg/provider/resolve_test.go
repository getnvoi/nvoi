package provider

import (
	"context"
	"fmt"
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

func TestResolveComputeUnknownProvider(t *testing.T) {
	_, err := ResolveCompute("no-such-provider", map[string]string{"token": "abc"})
	if err == nil {
		t.Fatal("unknown provider: got nil, want error")
	}
	if !strings.Contains(err.Error(), "no-such-provider") {
		t.Errorf("unknown provider: error %q should mention provider name", err)
	}
}

// ── CredentialSource + ResolveFrom tests ────────────────────────────────────

func TestResolveFrom_MapSource(t *testing.T) {
	schema := CredentialSchema{
		Name: "test",
		Fields: []CredentialField{
			{Key: "token", EnvVar: "MY_TOKEN"},
			{Key: "region", EnvVar: "MY_REGION"},
		},
	}
	source := MapSource{M: map[string]string{"MY_TOKEN": "abc", "MY_REGION": "us"}}
	creds, err := ResolveFrom(schema, source)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds["token"] != "abc" {
		t.Errorf("token = %q, want abc", creds["token"])
	}
	if creds["region"] != "us" {
		t.Errorf("region = %q, want us", creds["region"])
	}
}

func TestResolveFrom_MapSource_Missing(t *testing.T) {
	schema := CredentialSchema{
		Name: "test",
		Fields: []CredentialField{
			{Key: "token", EnvVar: "MY_TOKEN"},
			{Key: "region", EnvVar: "MY_REGION"},
		},
	}
	source := MapSource{M: map[string]string{"MY_TOKEN": "abc"}}
	creds, err := ResolveFrom(schema, source)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds["token"] != "abc" {
		t.Errorf("token = %q, want abc", creds["token"])
	}
	if _, ok := creds["region"]; ok {
		t.Error("missing key should not produce a value")
	}
}

func TestResolveFrom_MapSource_Empty(t *testing.T) {
	schema := CredentialSchema{
		Name: "test",
		Fields: []CredentialField{
			{Key: "token", EnvVar: "MY_TOKEN"},
		},
	}
	source := MapSource{M: map[string]string{"MY_TOKEN": ""}}
	creds, err := ResolveFrom(schema, source)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := creds["token"]; ok {
		t.Error("empty value should not produce a key")
	}
}

// errorSource is a CredentialSource that returns an error.
type errorSource struct{ err error }

func (s errorSource) Get(string) (string, error) { return "", s.err }

func TestResolveFrom_ErrorSource(t *testing.T) {
	schema := CredentialSchema{
		Name: "test",
		Fields: []CredentialField{
			{Key: "token", EnvVar: "MY_TOKEN"},
		},
	}
	source := errorSource{err: fmt.Errorf("connection refused")}
	_, err := ResolveFrom(schema, source)
	if err == nil {
		t.Fatal("expected error from failing source")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("error %q should mention underlying cause", err)
	}
	if !strings.Contains(err.Error(), "test") {
		t.Errorf("error %q should mention schema name", err)
	}
}
