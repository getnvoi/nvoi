package core

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/internal/testutil"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
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

type notFoundBucket struct {
	testutil.MockBucket
	deleted []string
}

func (b *notFoundBucket) DeleteBucket(ctx context.Context, name string) error {
	b.deleted = append(b.deleted, name)
	return utils.ErrNotFound
}

func TestStorageDelete_StillRemovesSecretsWhenBucketAlreadyGone(t *testing.T) {
	bucket := &notFoundBucket{}
	bucketProvider := fmt.Sprintf("storage-delete-test-%p", bucket)
	provider.RegisterBucket(bucketProvider, provider.CredentialSchema{Name: bucketProvider}, func(creds map[string]string) provider.BucketProvider {
		return bucket
	})

	ssh := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "get secret secrets 2>/dev/null", Result: testutil.MockResult{}},
			{Prefix: "get secret secrets -o jsonpath", Result: testutil.MockResult{
				Output: []byte(`'{"STORAGE_ASSETS_ENDPOINT":"a","STORAGE_ASSETS_BUCKET":"b","STORAGE_ASSETS_ACCESS_KEY_ID":"c","STORAGE_ASSETS_SECRET_ACCESS_KEY":"d"}'`),
			}},
			{Prefix: "patch secret", Result: testutil.MockResult{}},
		},
	}

	err := StorageDelete(context.Background(), StorageDeleteRequest{
		Cluster: testCluster(ssh),
		Storage: ProviderRef{Name: bucketProvider},
		Name:    "assets",
	})
	if err == nil || err != utils.ErrNotFound {
		t.Fatalf("StorageDelete should preserve already-gone signal after secret cleanup, got %v", err)
	}
	if len(bucket.deleted) != 1 {
		t.Fatalf("expected bucket delete attempt, got %v", bucket.deleted)
	}
	if len(ssh.Calls) == 0 {
		t.Fatal("expected cluster secret cleanup to still run")
	}
}
