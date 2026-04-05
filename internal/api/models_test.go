package api

import "testing"

func TestComputeProvider_Valid(t *testing.T) {
	valid := []ComputeProvider{ComputeHetzner, ComputeAWS, ComputeScaleway}
	for _, p := range valid {
		if !p.Valid() {
			t.Errorf("%q should be valid", p)
		}
	}
	if ComputeProvider("digitalocean").Valid() {
		t.Error("digitalocean should not be valid")
	}
	if ComputeProvider("").Valid() {
		t.Error("empty should not be valid")
	}
}

func TestDNSProvider_Valid(t *testing.T) {
	valid := []DNSProvider{DNSCloudflare, DNSAWS}
	for _, p := range valid {
		if !p.Valid() {
			t.Errorf("%q should be valid", p)
		}
	}
	if DNSProvider("godaddy").Valid() {
		t.Error("godaddy should not be valid")
	}
}

func TestStorageProvider_Valid(t *testing.T) {
	valid := []StorageProvider{StorageCloudflare, StorageAWS}
	for _, p := range valid {
		if !p.Valid() {
			t.Errorf("%q should be valid", p)
		}
	}
	if StorageProvider("gcs").Valid() {
		t.Error("gcs should not be valid")
	}
}

func TestBuildProvider_Valid(t *testing.T) {
	valid := []BuildProvider{BuildLocal, BuildDaytona, BuildGitHub}
	for _, p := range valid {
		if !validBuildProviders[p] {
			t.Errorf("%q should be valid", p)
		}
	}
	if validBuildProviders[BuildProvider("circleci")] {
		t.Error("circleci should not be valid")
	}
}

func TestRepoConfig_ValidateProviders(t *testing.T) {
	// Valid: all set.
	rc := &RepoConfig{
		ComputeProvider: ComputeHetzner,
		DNSProvider:     DNSCloudflare,
		StorageProvider: StorageAWS,
		BuildProvider:   BuildDaytona,
	}
	if err := rc.ValidateProviders(); err != nil {
		t.Errorf("all valid: %v", err)
	}

	// Valid: only compute (others optional).
	rc = &RepoConfig{ComputeProvider: ComputeAWS}
	if err := rc.ValidateProviders(); err != nil {
		t.Errorf("compute only: %v", err)
	}

	// Invalid compute.
	rc = &RepoConfig{ComputeProvider: "nope"}
	if err := rc.ValidateProviders(); err == nil {
		t.Error("expected error for invalid compute provider")
	}

	// Invalid DNS.
	rc = &RepoConfig{ComputeProvider: ComputeHetzner, DNSProvider: "nope"}
	if err := rc.ValidateProviders(); err == nil {
		t.Error("expected error for invalid dns provider")
	}

	// Invalid storage.
	rc = &RepoConfig{ComputeProvider: ComputeHetzner, StorageProvider: "nope"}
	if err := rc.ValidateProviders(); err == nil {
		t.Error("expected error for invalid storage provider")
	}

	// Invalid build.
	rc = &RepoConfig{ComputeProvider: ComputeHetzner, BuildProvider: "nope"}
	if err := rc.ValidateProviders(); err == nil {
		t.Error("expected error for invalid build provider")
	}
}
