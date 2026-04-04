package core

import (
	"context"
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/internal/testutil"
)

func TestServiceSet_MissingImage(t *testing.T) {
	err := ServiceSet(context.Background(), ServiceSetRequest{
		Cluster: testCluster(&testutil.MockSSH{}),
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

func TestServiceSet_MissingSecret(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "create namespace", Result: testutil.MockResult{}},
			// ListSecretKeys returns empty — no secrets exist
			{Prefix: "get secret secrets -o jsonpath", Result: testutil.MockResult{
				Output: []byte("'{}'"),
			}},
		},
	}

	err := ServiceSet(context.Background(), ServiceSetRequest{
		Cluster: testCluster(mock),
		Name:    "web",
		Image:   "myapp:latest",
		Secrets: []string{"NONEXISTENT"},
	})
	if err == nil {
		t.Fatal("ServiceSet: expected error for missing secret, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("ServiceSet: error = %q, want it to contain %q", err.Error(), "not found")
	}
}
