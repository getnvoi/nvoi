package cloudflare

import (
	"net/http"
	"testing"
)

func TestNewAPI(t *testing.T) {
	api := NewAPI("test-key", "cloudflare dns")
	if api == nil {
		t.Fatal("NewAPI returned nil")
	}
	if api.BaseURL != BaseURL {
		t.Errorf("BaseURL = %q, want %q", api.BaseURL, BaseURL)
	}
	if api.Label != "cloudflare dns" {
		t.Errorf("Label = %q, want %q", api.Label, "cloudflare dns")
	}
	if api.SetAuth == nil {
		t.Fatal("SetAuth is nil")
	}
}

func TestNewAPI_SetsAuthHeader(t *testing.T) {
	api := NewAPI("my-secret-key", "test")
	req, _ := http.NewRequest("GET", "https://example.com", nil)
	api.SetAuth(req)
	got := req.Header.Get("Authorization")
	want := "Bearer my-secret-key"
	if got != want {
		t.Errorf("Authorization = %q, want %q", got, want)
	}
}

func TestBaseURL(t *testing.T) {
	if BaseURL != "https://api.cloudflare.com/client/v4" {
		t.Errorf("BaseURL = %q, want %q", BaseURL, "https://api.cloudflare.com/client/v4")
	}
}
