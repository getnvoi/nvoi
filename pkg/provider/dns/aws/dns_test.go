package aws

import (
	"testing"

	"github.com/getnvoi/nvoi/pkg/provider"
)

func TestResolveDNS(t *testing.T) {
	creds := map[string]string{
		"access_key_id":     "AKIAIOSFODNN7EXAMPLE",
		"secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		"region":            "us-east-1",
		"zone":              "example.com",
	}
	p, err := provider.ResolveDNS("aws", creds)
	if err != nil {
		t.Fatalf("ResolveDNS with valid creds: %v", err)
	}
	if p == nil {
		t.Fatal("ResolveDNS returned nil provider")
	}
}

func TestResolveDNS_MissingZone(t *testing.T) {
	creds := map[string]string{
		"access_key_id":     "AKIAIOSFODNN7EXAMPLE",
		"secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		"region":            "us-east-1",
	}
	_, err := provider.ResolveDNS("aws", creds)
	if err == nil {
		t.Fatal("expected error for missing zone")
	}
	if !contains(err.Error(), "zone") {
		t.Errorf("error %q should mention zone", err.Error())
	}
}

// --- helpers ---

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
