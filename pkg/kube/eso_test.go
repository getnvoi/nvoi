package kube

import (
	"strings"
	"testing"
)

func init() {
	// Register test ESO providers for YAML generation tests.
	RegisterESOProvider("test-provider", func(authName string, creds map[string]string) map[string]any {
		return map[string]any{
			"testProvider": map[string]any{
				"auth": map[string]any{
					"secretRef": map[string]any{
						"name": authName,
						"key":  "token",
					},
				},
				"region": creds["region"],
			},
		}
	})
}

func TestGenerateSecretStoreYAML(t *testing.T) {
	yaml, err := GenerateSecretStoreYAML("my-store", "my-ns", "test-provider", "my-auth", map[string]string{"region": "us-east-1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"kind: SecretStore",
		"name: my-store",
		"namespace: my-ns",
		"testProvider",
		"name: my-auth",
		"region: us-east-1",
	} {
		if !strings.Contains(yaml, want) {
			t.Errorf("missing %q in:\n%s", want, yaml)
		}
	}
}

func TestGenerateSecretStoreYAML_Unknown(t *testing.T) {
	_, err := GenerateSecretStoreYAML("s", "ns", "vault", "auth", nil)
	if err == nil {
		t.Fatal("expected error for unregistered provider")
	}
}

func TestGenerateExternalSecretYAML(t *testing.T) {
	yaml, err := GenerateExternalSecretYAML(ExternalSecretSpec{
		Name:            "api-secrets",
		StoreName:       "nvoi-secrets",
		Keys:            []string{"JWT_SECRET", "ENCRYPTION_KEY"},
		RefreshInterval: "1h",
	}, "nvoi-myapp-prod")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"kind: ExternalSecret",
		"name: api-secrets",
		"namespace: nvoi-myapp-prod",
		"refreshInterval: 1h",
		"name: nvoi-secrets",
		"kind: SecretStore",
		"secretKey: JWT_SECRET",
		"key: JWT_SECRET",
		"secretKey: ENCRYPTION_KEY",
		"creationPolicy: Owner",
	} {
		if !strings.Contains(yaml, want) {
			t.Errorf("missing %q in:\n%s", want, yaml)
		}
	}
}

func TestGenerateExternalSecretYAML_Static(t *testing.T) {
	yaml, err := GenerateExternalSecretYAML(ExternalSecretSpec{
		Name:            "db-secrets",
		StoreName:       "nvoi-secrets",
		Keys:            []string{"DATABASE_URL"},
		RefreshInterval: "0",
	}, "ns")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(yaml, "refreshInterval: \"0\"") && !strings.Contains(yaml, "refreshInterval: 0") {
		t.Errorf("static secret should have refreshInterval 0, got:\n%s", yaml)
	}
}
