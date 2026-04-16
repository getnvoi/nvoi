package core

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/testutil"
	"github.com/getnvoi/nvoi/pkg/utils"
)

func TestStorageList_FromConfig(t *testing.T) {
	items, err := StorageList(context.Background(), StorageListRequest{
		Cluster:      testCluster(nil),
		StorageNames: []string{"assets", "backups"},
	})
	if err != nil {
		t.Fatalf("StorageList: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("StorageList: got %d items, want 2", len(items))
	}
	if items[0].Name != "assets" {
		t.Errorf("items[0].Name = %q, want assets", items[0].Name)
	}
	if items[1].Name != "backups" {
		t.Errorf("items[1].Name = %q, want backups", items[1].Name)
	}
	// Bucket names are derived from Names.Bucket()
	if !strings.Contains(items[0].Bucket, "assets") {
		t.Errorf("items[0].Bucket = %q, should contain 'assets'", items[0].Bucket)
	}
}

func TestStorageList_Empty(t *testing.T) {
	items, err := StorageList(context.Background(), StorageListRequest{
		Cluster: testCluster(nil),
	})
	if err != nil {
		t.Fatalf("StorageList: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("StorageList: got %d items, want 0", len(items))
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

	ssh := &testutil.MockSSH{}

	err := StorageDelete(context.Background(), StorageDeleteRequest{
		Cluster: testCluster(ssh),
		Storage: ProviderRef{Name: bucketProvider},
		Name:    "assets",
	})
	if err == nil || err != utils.ErrNotFound {
		t.Fatalf("StorageDelete should return ErrNotFound when bucket already gone, got %v", err)
	}
	if len(bucket.deleted) != 1 {
		t.Fatalf("expected bucket delete attempt, got %v", bucket.deleted)
	}
}
