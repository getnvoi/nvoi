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

func TestServiceDelete_BlockedWhenIngressStillTargetsService(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "get configmap", Result: testutil.MockResult{
				Output: []byte("'example.com {\n\treverse_proxy web.ns.svc.cluster.local:3000\n}'"),
			}},
		},
	}

	err := ServiceDelete(context.Background(), ServiceDeleteRequest{
		Cluster: testCluster(mock),
		Name:    "web",
	})
	if err == nil {
		t.Fatal("expected service delete guard to block deletion")
	}
	if !strings.Contains(err.Error(), "service delete blocked") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "remove or reconcile ingress first") {
		t.Fatalf("guard should instruct next step, got: %v", err)
	}
}

func TestServiceDelete_AllowsDeletionWhenIngressDoesNotTargetService(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "get configmap", Result: testutil.MockResult{
				Output: []byte("'example.com {\n\treverse_proxy api.ns.svc.cluster.local:3000\n}'"),
			}},
			{Prefix: "delete deployment/", Result: testutil.MockResult{}},
			{Prefix: "delete statefulset/", Result: testutil.MockResult{}},
			{Prefix: "delete service/", Result: testutil.MockResult{}},
		},
	}

	err := ServiceDelete(context.Background(), ServiceDeleteRequest{
		Cluster: testCluster(mock),
		Name:    "web",
	})
	if err != nil {
		t.Fatalf("service delete should succeed when ingress no longer targets service: %v", err)
	}
}
