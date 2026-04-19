package aws

import (
	"testing"

	"github.com/getnvoi/nvoi/pkg/provider"
)

func TestNewBucket_StoresCredentials(t *testing.T) {
	creds := map[string]string{
		"access_key_id":     "AKIAIOSFODNN7EXAMPLE",
		"secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		"region":            "us-east-1",
	}
	b := NewBucket(creds)
	if b == nil {
		t.Fatal("NewBucket returned nil")
	}
	if b.configErr != nil {
		t.Fatalf("unexpected config error: %v", b.configErr)
	}
	if b.region != "us-east-1" {
		t.Errorf("region = %q, want %q", b.region, "us-east-1")
	}
	if b.accessKeyID != "AKIAIOSFODNN7EXAMPLE" {
		t.Errorf("accessKeyID = %q, want %q", b.accessKeyID, "AKIAIOSFODNN7EXAMPLE")
	}
}

func TestCredentials_ReturnsEndpoint(t *testing.T) {
	creds := map[string]string{
		"access_key_id":     "AKIAIOSFODNN7EXAMPLE",
		"secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		"region":            "eu-west-1",
	}
	b := NewBucket(creds)
	bc, err := b.Credentials(nil)
	if err != nil {
		t.Fatalf("Credentials: %v", err)
	}
	wantEndpoint := "https://s3.eu-west-1.amazonaws.com"
	if bc.Endpoint != wantEndpoint {
		t.Errorf("Endpoint = %q, want %q", bc.Endpoint, wantEndpoint)
	}
	if bc.Region != "eu-west-1" {
		t.Errorf("Region = %q, want %q", bc.Region, "eu-west-1")
	}
	if bc.AccessKeyID != "AKIAIOSFODNN7EXAMPLE" {
		t.Errorf("AccessKeyID = %q, want %q", bc.AccessKeyID, "AKIAIOSFODNN7EXAMPLE")
	}
}

func TestResolveBucket_Registered(t *testing.T) {
	// init() in register.go registers "aws" — verify it resolves with valid creds.
	creds := map[string]string{
		"access_key_id":     "AKIAIOSFODNN7EXAMPLE",
		"secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		"region":            "us-east-1",
	}
	p, err := provider.ResolveBucket("aws", creds)
	if err != nil {
		t.Fatalf("ResolveBucket with valid creds: %v", err)
	}
	if p == nil {
		t.Fatal("ResolveBucket returned nil provider")
	}
}

func TestIsS3BucketExists(t *testing.T) {
	if isS3BucketExists(nil) {
		t.Error("nil error should not be BucketExists")
	}
}

func TestIsS3NotFound(t *testing.T) {
	if isS3NotFound(nil) {
		t.Error("nil error should not be NotFound")
	}
}

