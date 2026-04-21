package aws

import (
	"testing"
)

func TestLoadConfig_ValidCreds(t *testing.T) {
	creds := map[string]string{
		"access_key_id":     "AKIAIOSFODNN7EXAMPLE",
		"secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		"region":            "us-east-1",
	}
	cfg, err := LoadConfig(creds)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Region != "us-east-1" {
		t.Errorf("Region = %q, want %q", cfg.Region, "us-east-1")
	}
}

func TestLoadConfig_EmptyCreds(t *testing.T) {
	// Empty creds should still produce a config (validation happens downstream).
	// The SDK may pick up AWS_REGION from the environment, so we just check
	// that LoadConfig doesn't error — credential validation is the provider's job.
	_, err := LoadConfig(map[string]string{})
	if err != nil {
		t.Fatalf("LoadConfig with empty creds: %v", err)
	}
}
