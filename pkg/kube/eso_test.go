package kube

import (
	"strings"
	"testing"
)

func TestGenerateSecretStoreYAML_AWS(t *testing.T) {
	yaml, err := GenerateSecretStoreYAML(SecretStoreSpec{
		Name: "nvoi-secrets", Kind: "awssm", AuthName: "nvoi-eso-auth",
	}, "nvoi-myapp-prod")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"kind: SecretStore",
		"name: nvoi-secrets",
		"namespace: nvoi-myapp-prod",
		"SecretsManager",
		"name: nvoi-eso-auth",
		"access_key_id",
		"secret_access_key",
	} {
		if !strings.Contains(yaml, want) {
			t.Errorf("missing %q in:\n%s", want, yaml)
		}
	}
}

func TestGenerateSecretStoreYAML_Doppler(t *testing.T) {
	yaml, err := GenerateSecretStoreYAML(SecretStoreSpec{
		Name: "nvoi-secrets", Kind: "doppler", AuthName: "nvoi-eso-auth",
	}, "nvoi-myapp-prod")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"kind: SecretStore",
		"doppler:",
		"dopplerToken:",
		"name: nvoi-eso-auth",
		"key: token",
	} {
		if !strings.Contains(yaml, want) {
			t.Errorf("missing %q in:\n%s", want, yaml)
		}
	}
}

func TestGenerateSecretStoreYAML_Scaleway(t *testing.T) {
	yaml, err := GenerateSecretStoreYAML(SecretStoreSpec{
		Name: "nvoi-secrets", Kind: "scaleway", AuthName: "nvoi-eso-auth",
	}, "ns")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"scaleway:", "access_key", "secret_key", "project_id"} {
		if !strings.Contains(yaml, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestGenerateSecretStoreYAML_Infisical(t *testing.T) {
	yaml, err := GenerateSecretStoreYAML(SecretStoreSpec{
		Name: "nvoi-secrets", Kind: "infisical", AuthName: "nvoi-eso-auth",
	}, "ns")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"infisical:", "key: token"} {
		if !strings.Contains(yaml, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestGenerateSecretStoreYAML_Unknown(t *testing.T) {
	_, err := GenerateSecretStoreYAML(SecretStoreSpec{Kind: "vault"}, "ns")
	if err == nil {
		t.Fatal("expected error for unsupported kind")
	}
}

func TestGenerateExternalSecretYAML(t *testing.T) {
	yaml := GenerateExternalSecretYAML(ExternalSecretSpec{
		Name:            "api-secrets",
		StoreName:       "nvoi-secrets",
		Keys:            []string{"JWT_SECRET", "ENCRYPTION_KEY"},
		RefreshInterval: "1h",
	}, "nvoi-myapp-prod")

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
		"key: ENCRYPTION_KEY",
		"creationPolicy: Owner",
	} {
		if !strings.Contains(yaml, want) {
			t.Errorf("missing %q in:\n%s", want, yaml)
		}
	}
}

func TestGenerateExternalSecretYAML_Static(t *testing.T) {
	yaml := GenerateExternalSecretYAML(ExternalSecretSpec{
		Name:            "db-secrets",
		StoreName:       "nvoi-secrets",
		Keys:            []string{"DATABASE_URL"},
		RefreshInterval: "0",
	}, "ns")

	if !strings.Contains(yaml, "refreshInterval: 0") {
		t.Errorf("static secret should have refreshInterval: 0, got:\n%s", yaml)
	}
}
