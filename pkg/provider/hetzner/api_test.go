package hetzner

import (
	"testing"
)

func TestNewAPI(t *testing.T) {
	api := NewAPI("test-token")
	if api == nil {
		t.Fatal("NewAPI returned nil")
	}
	if api.BaseURL != BaseURL {
		t.Errorf("BaseURL = %q, want %q", api.BaseURL, BaseURL)
	}
	if api.Label != "hetzner" {
		t.Errorf("Label = %q, want %q", api.Label, "hetzner")
	}
	if api.SetAuth == nil {
		t.Fatal("SetAuth is nil")
	}
}

func TestBaseURL(t *testing.T) {
	if BaseURL != "https://api.hetzner.cloud/v1" {
		t.Errorf("BaseURL = %q, want %q", BaseURL, "https://api.hetzner.cloud/v1")
	}
}
