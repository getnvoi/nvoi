package core

import (
	"context"
	"strings"
	"testing"
)

func TestServiceSet_MissingImage(t *testing.T) {
	err := ServiceSet(context.Background(), ServiceSetRequest{
		Cluster: testCluster(),
		Name:    "web",
		Image:   "",
	})
	if err == nil {
		t.Fatal("ServiceSet: expected error for missing image, got nil")
	}
	if !strings.Contains(err.Error(), "--image is required") {
		t.Errorf("ServiceSet: error = %q, want it to contain %q", err.Error(), "--image is required")
	}
}

func TestServiceDelete_Succeeds(t *testing.T) {
	err := ServiceDelete(context.Background(), ServiceDeleteRequest{
		Cluster: testCluster(),
		Name:    "web",
	})
	if err != nil {
		t.Fatalf("service delete should succeed: %v", err)
	}
}
