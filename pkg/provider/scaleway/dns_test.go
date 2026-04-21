package scaleway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestEnsureCNAME_RemovesAddressRecordsFirst(t *testing.T) {
	var got patchRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/dns-zones/example.com/records" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode patch request: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	c := NewDNS(map[string]string{"secret_key": "test-key", "zone": "example.com"})
	c.api.BaseURL = ts.URL
	c.api.HTTPClient = ts.Client()

	if err := c.ensureCNAME(context.Background(), "app.example.com", "target.example.net"); err != nil {
		t.Fatalf("ensureCNAME: %v", err)
	}

	if len(got.Changes) != 3 {
		t.Fatalf("changes = %d, want 3", len(got.Changes))
	}
	if got.Changes[0].Delete == nil || got.Changes[0].Delete.Type != "A" {
		t.Fatalf("first change = %#v, want delete A", got.Changes[0])
	}
	if got.Changes[1].Delete == nil || got.Changes[1].Delete.Type != "AAAA" {
		t.Fatalf("second change = %#v, want delete AAAA", got.Changes[1])
	}
	if got.Changes[2].Set == nil || got.Changes[2].Set.Type != "CNAME" {
		t.Fatalf("third change = %#v, want set CNAME", got.Changes[2])
	}
}

func TestEnsureAddress_RemovesCNAMEFirst(t *testing.T) {
	var got patchRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/dns-zones/example.com/records" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode patch request: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	c := NewDNS(map[string]string{"secret_key": "test-key", "zone": "example.com"})
	c.api.BaseURL = ts.URL
	c.api.HTTPClient = ts.Client()

	if err := c.ensureAddress(context.Background(), "app.example.com", "203.0.113.10"); err != nil {
		t.Fatalf("ensureAddress: %v", err)
	}

	if len(got.Changes) != 2 {
		t.Fatalf("changes = %d, want 2", len(got.Changes))
	}
	if got.Changes[0].Delete == nil || got.Changes[0].Delete.Type != "CNAME" {
		t.Fatalf("first change = %#v, want delete CNAME", got.Changes[0])
	}
	if got.Changes[1].Set == nil || got.Changes[1].Set.Type != "A" {
		t.Fatalf("second change = %#v, want set A", got.Changes[1])
	}
}
