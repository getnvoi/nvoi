package provider

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// stubInfra is the minimal InfraProvider used by registry tests. It
// satisfies the interface so RegisterInfra/ResolveInfra can be exercised
// without spinning a real provider. The methods all return zero values —
// these tests check the registry plumbing, not orchestration semantics.
type stubInfra struct{}

func (stubInfra) Connect(context.Context, *BootstrapContext) (*kube.Client, error) {
	return nil, nil
}
func (stubInfra) Bootstrap(context.Context, *BootstrapContext) (*kube.Client, error) {
	return nil, nil
}
func (stubInfra) LiveSnapshot(context.Context, *BootstrapContext) (*LiveSnapshot, error) {
	return nil, nil
}
func (stubInfra) TeardownOrphans(context.Context, *BootstrapContext) error {
	return nil
}
func (stubInfra) Teardown(context.Context, *BootstrapContext, bool) error { return nil }
func (stubInfra) IngressBinding(context.Context, *BootstrapContext, ServiceTarget) (IngressBinding, error) {
	return IngressBinding{}, nil
}
func (stubInfra) HasPublicIngress() bool                                 { return false }
func (stubInfra) ConsumesBlocks() []string                               { return nil }
func (stubInfra) ValidateConfig(ProviderConfigView) error                { return nil }
func (stubInfra) ListResources(context.Context) ([]ResourceGroup, error) { return nil, nil }
func (stubInfra) NodeShell(context.Context, *BootstrapContext) (utils.SSHClient, error) {
	return nil, nil
}
func (stubInfra) ValidateCredentials(context.Context) error { return nil }
func (stubInfra) Close() error                              { return nil }
func (stubInfra) ArchForType(string) string                 { return "amd64" }

func init() {
	RegisterInfra("test-infra", CredentialSchema{
		Name: "test-infra",
		Fields: []CredentialField{
			{Key: "token", Required: true, EnvVar: "TEST_TOKEN", Flag: "token"},
			{Key: "region", Required: false, EnvVar: "TEST_REGION", Flag: "region"},
		},
	}, func(creds map[string]string) InfraProvider {
		return stubInfra{}
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

func TestResolveInfraValid(t *testing.T) {
	p, err := ResolveInfra("test-infra", map[string]string{"token": "abc123"})
	if err != nil {
		t.Fatalf("valid creds: got error %v, want nil", err)
	}
	if p == nil {
		t.Fatal("valid creds: got nil provider")
	}
}

func TestResolveInfraInvalidCreds(t *testing.T) {
	_, err := ResolveInfra("test-infra", map[string]string{})
	if err == nil {
		t.Fatal("invalid creds: got nil, want error")
	}
	if !strings.Contains(err.Error(), "token") {
		t.Errorf("invalid creds: error %q should mention missing field %q", err, "token")
	}
}

func TestResolveInfraUnknownProvider(t *testing.T) {
	_, err := ResolveInfra("no-such-provider", map[string]string{"token": "abc"})
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
