package scaleway

import (
	"testing"

	"github.com/getnvoi/nvoi/pkg/provider"
)

func TestNewDNS_BaseURL(t *testing.T) {
	c := NewDNS(map[string]string{"secret_key": "test-key", "zone": "example.com"})
	want := BaseURL + "/domain/v2beta1"
	if c.api.BaseURL != want {
		t.Errorf("BaseURL = %q, want %q", c.api.BaseURL, want)
	}
}

func TestNewDNS_Zone(t *testing.T) {
	c := NewDNS(map[string]string{"secret_key": "test-key", "zone": "myapp.com"})
	if c.zone != "myapp.com" {
		t.Errorf("zone = %q, want %q", c.zone, "myapp.com")
	}
}

func TestValidateCredentials_MissingZone(t *testing.T) {
	c := NewDNS(map[string]string{"secret_key": "test-key"})
	err := c.ValidateCredentials(nil)
	if err == nil {
		t.Fatal("expected error for missing zone")
	}
	if !contains(err.Error(), "zone") {
		t.Errorf("error %q should mention zone", err.Error())
	}
}

func TestResolveDNS_Registered(t *testing.T) {
	// init() in register.go registers "scaleway" DNS — verify registration
	creds := map[string]string{
		"secret_key": "test-key",
		"zone":       "example.com",
	}
	p, err := provider.ResolveDNS("scaleway", creds)
	if err != nil {
		t.Fatalf("ResolveDNS with valid creds: %v", err)
	}
	if p == nil {
		t.Fatal("ResolveDNS returned nil provider")
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
