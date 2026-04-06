package handlers

import (
	"testing"

	"github.com/getnvoi/nvoi/internal/api"

	// Providers must be registered for resolveAllCredentials to map credentials.
	_ "github.com/getnvoi/nvoi/pkg/provider/aws"
	_ "github.com/getnvoi/nvoi/pkg/provider/cloudflare"
	_ "github.com/getnvoi/nvoi/pkg/provider/daytona"
	_ "github.com/getnvoi/nvoi/pkg/provider/hetzner"
	_ "github.com/getnvoi/nvoi/pkg/provider/local"
	_ "github.com/getnvoi/nvoi/pkg/provider/scaleway"
)

func TestResolveAllCredentials_AllProviders(t *testing.T) {
	rc := &api.RepoConfig{
		ComputeProvider: api.ComputeHetzner,
		DNSProvider:     api.DNSCloudflare,
		StorageProvider: api.StorageAWS,
		BuildProvider:   api.BuildDaytona,
	}
	env := map[string]string{
		"HETZNER_TOKEN":         "tok123",
		"CF_API_KEY":            "cfkey",
		"CF_ZONE_ID":            "zone1",
		"DNS_ZONE":              "example.com",
		"CF_ACCOUNT_ID":         "cfacct",
		"AWS_ACCESS_KEY_ID":     "awskey",
		"AWS_SECRET_ACCESS_KEY": "awssecret",
		"DAYTONA_API_KEY":       "daykey",
	}

	creds, err := resolveAllCredentials(rc, env)
	if err != nil {
		t.Fatalf("resolveAllCredentials: %v", err)
	}

	// Compute: HETZNER_TOKEN -> token
	if creds.Compute["token"] != "tok123" {
		t.Errorf("Compute[token] = %q, want tok123", creds.Compute["token"])
	}

	// DNS: CF_API_KEY -> api_key, DNS_ZONE -> zone
	if creds.DNS == nil {
		t.Fatal("DNS creds should not be nil")
	}
	if creds.DNS["api_key"] != "cfkey" {
		t.Errorf("DNS[api_key] = %q, want cfkey", creds.DNS["api_key"])
	}

	// Storage: AWS creds
	if creds.Storage == nil {
		t.Fatal("Storage creds should not be nil")
	}
	if creds.Storage["access_key_id"] != "awskey" {
		t.Errorf("Storage[access_key_id] = %q, want awskey", creds.Storage["access_key_id"])
	}

	// Build: DAYTONA_API_KEY -> api_key
	if creds.Build == nil {
		t.Fatal("Build creds should not be nil")
	}
	if creds.Build["api_key"] != "daykey" {
		t.Errorf("Build[api_key] = %q, want daykey", creds.Build["api_key"])
	}
}

func TestResolveAllCredentials_OptionalProvidersEmpty(t *testing.T) {
	rc := &api.RepoConfig{
		ComputeProvider: api.ComputeHetzner,
		// DNS, Storage, Build all empty
	}
	env := map[string]string{
		"HETZNER_TOKEN": "tok123",
	}

	creds, err := resolveAllCredentials(rc, env)
	if err != nil {
		t.Fatalf("resolveAllCredentials: %v", err)
	}

	if creds.Compute["token"] != "tok123" {
		t.Errorf("Compute[token] = %q, want tok123", creds.Compute["token"])
	}
	if creds.DNS != nil {
		t.Errorf("DNS should be nil, got %v", creds.DNS)
	}
	if creds.Storage != nil {
		t.Errorf("Storage should be nil, got %v", creds.Storage)
	}
	if creds.Build != nil {
		t.Errorf("Build should be nil, got %v", creds.Build)
	}
}

func TestResolveAllCredentials_UnknownComputeProvider(t *testing.T) {
	rc := &api.RepoConfig{
		ComputeProvider: "nonexistent",
	}

	_, err := resolveAllCredentials(rc, map[string]string{})
	if err == nil {
		t.Fatal("expected error for unknown compute provider")
	}
}

func TestResolveAllCredentials_UnknownDNSProvider(t *testing.T) {
	rc := &api.RepoConfig{
		ComputeProvider: api.ComputeHetzner,
		DNSProvider:     "bogus",
	}
	env := map[string]string{
		"HETZNER_TOKEN": "tok",
	}

	_, err := resolveAllCredentials(rc, env)
	if err == nil {
		t.Fatal("expected error for unknown DNS provider")
	}
}

func TestResolveAllCredentials_UnknownStorageProvider(t *testing.T) {
	rc := &api.RepoConfig{
		ComputeProvider: api.ComputeHetzner,
		StorageProvider: "bogus",
	}
	env := map[string]string{
		"HETZNER_TOKEN": "tok",
	}

	_, err := resolveAllCredentials(rc, env)
	if err == nil {
		t.Fatal("expected error for unknown storage provider")
	}
}

func TestResolveAllCredentials_UnknownBuildProvider(t *testing.T) {
	rc := &api.RepoConfig{
		ComputeProvider: api.ComputeHetzner,
		BuildProvider:   "bogus",
	}
	env := map[string]string{
		"HETZNER_TOKEN": "tok",
	}

	_, err := resolveAllCredentials(rc, env)
	if err == nil {
		t.Fatal("expected error for unknown build provider")
	}
}
