package core

import (
	"context"
	"testing"
)

func TestStorageList_ConfigDriven(t *testing.T) {
	items, err := StorageList(context.Background(), StorageListRequest{
		Cluster:      testCluster(),
		StorageNames: []string{"releases", "uploads"},
	})
	if err != nil {
		t.Fatalf("StorageList: unexpected error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("StorageList: got %d items, want 2", len(items))
	}
	if items[0].Name != "releases" {
		t.Errorf("items[0].Name = %q, want releases", items[0].Name)
	}
	if items[1].Name != "uploads" {
		t.Errorf("items[1].Name = %q, want uploads", items[1].Name)
	}
}

func TestStorageList_NoStorageNames(t *testing.T) {
	items, err := StorageList(context.Background(), StorageListRequest{
		Cluster: testCluster(),
	})
	if err != nil {
		t.Fatalf("StorageList: unexpected error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("StorageList: got %d items, want 0", len(items))
	}
}
