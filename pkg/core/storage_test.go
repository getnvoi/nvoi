package core

import (
	"strings"
	"testing"
)

func TestParseStorageBucketKey(t *testing.T) {
	tests := []struct {
		key      string
		wantName string
		wantOK   bool
	}{
		{"STORAGE_MYDB_BUCKET", "mydb", true},
		{"STORAGE_ASSETS_BUCKET", "assets", true},
		{"OTHER_MYDB_BUCKET", "", false},
		{"STORAGE_MYDB_ENDPOINT", "", false},
		{"STORAGE__BUCKET", "", false},
	}
	for _, tt := range tests {
		name, ok := parseStorageBucketKey(tt.key)
		if ok != tt.wantOK {
			t.Errorf("parseStorageBucketKey(%q): ok = %v, want %v", tt.key, ok, tt.wantOK)
			continue
		}
		if name != tt.wantName {
			t.Errorf("parseStorageBucketKey(%q): name = %q, want %q", tt.key, name, tt.wantName)
		}
	}
}

func TestStorageSecretKeys(t *testing.T) {
	keys := StorageSecretKeys("assets")
	if len(keys) != 4 {
		t.Fatalf("StorageSecretKeys(\"assets\"): got %d keys, want 4", len(keys))
	}

	expectedPrefix := "STORAGE_ASSETS_"
	for _, key := range keys {
		if !strings.HasPrefix(key, expectedPrefix) {
			t.Errorf("StorageSecretKeys(\"assets\"): key %q missing prefix %q", key, expectedPrefix)
		}
	}

	expectedSuffixes := []string{"_ENDPOINT", "_BUCKET", "_ACCESS_KEY_ID", "_SECRET_ACCESS_KEY"}
	for _, suffix := range expectedSuffixes {
		found := false
		for _, key := range keys {
			if strings.HasSuffix(key, suffix) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("StorageSecretKeys(\"assets\"): no key with suffix %q in %v", suffix, keys)
		}
	}
}
