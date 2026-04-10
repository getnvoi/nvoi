package scwbase

import (
	"net/http"
	"testing"
)

func TestNewAPI(t *testing.T) {
	api := NewAPI("test-secret", "scaleway dns")
	if api == nil {
		t.Fatal("NewAPI returned nil")
	}
	if api.BaseURL != BaseURL {
		t.Errorf("BaseURL = %q, want %q", api.BaseURL, BaseURL)
	}
	if api.Label != "scaleway dns" {
		t.Errorf("Label = %q, want %q", api.Label, "scaleway dns")
	}
	if api.SetAuth == nil {
		t.Fatal("SetAuth is nil")
	}
}

func TestNewAPI_SetsAuthHeader(t *testing.T) {
	api := NewAPI("my-secret-key", "test")
	req, _ := http.NewRequest("GET", "https://example.com", nil)
	api.SetAuth(req)
	got := req.Header.Get("X-Auth-Token")
	want := "my-secret-key"
	if got != want {
		t.Errorf("X-Auth-Token = %q, want %q", got, want)
	}
}

func TestBaseURL(t *testing.T) {
	if BaseURL != "https://api.scaleway.com" {
		t.Errorf("BaseURL = %q, want %q", BaseURL, "https://api.scaleway.com")
	}
}
