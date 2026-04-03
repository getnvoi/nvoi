package app

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/getnvoi/nvoi/internal/testutil"
)

func TestStorageList_ReturnsItems(t *testing.T) {
	bucketB64 := base64.StdEncoding.EncodeToString([]byte("nvoi-myapp-prod-assets"))
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			// ListSecretKeys — returns keys including a STORAGE_*_BUCKET key
			{Prefix: "get secret secrets -o jsonpath='{.data}'", Result: testutil.MockResult{
				Output: []byte(`'{"STORAGE_ASSETS_ENDPOINT":"` + base64.StdEncoding.EncodeToString([]byte("https://r2.example.com")) + `","STORAGE_ASSETS_BUCKET":"` + bucketB64 + `","STORAGE_ASSETS_ACCESS_KEY_ID":"` + base64.StdEncoding.EncodeToString([]byte("ak")) + `","STORAGE_ASSETS_SECRET_ACCESS_KEY":"` + base64.StdEncoding.EncodeToString([]byte("sk")) + `"}'`),
			}},
			// GetSecretValue — returns the bucket name (base64 encoded)
			{Prefix: "get secret secrets -o jsonpath='{.data.STORAGE_ASSETS_BUCKET}'", Result: testutil.MockResult{
				Output: []byte("'" + bucketB64 + "'"),
			}},
		},
	}

	items, err := StorageList(context.Background(), StorageListRequest{
		Cluster: testCluster(mock),
	})
	if err != nil {
		t.Fatalf("StorageList: unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("StorageList: got %d items, want 1", len(items))
	}
	if items[0].Name != "assets" {
		t.Errorf("StorageList: item name = %q, want %q", items[0].Name, "assets")
	}
	if items[0].Bucket != "nvoi-myapp-prod-assets" {
		t.Errorf("StorageList: item bucket = %q, want %q", items[0].Bucket, "nvoi-myapp-prod-assets")
	}
}

func TestStorageList_Empty(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "get secret secrets -o jsonpath", Result: testutil.MockResult{
				Output: []byte("'{}'"),
			}},
		},
	}

	items, err := StorageList(context.Background(), StorageListRequest{
		Cluster: testCluster(mock),
	})
	if err != nil {
		t.Fatalf("StorageList: unexpected error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("StorageList: got %d items, want 0", len(items))
	}
}
