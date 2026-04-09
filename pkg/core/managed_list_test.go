package core

import (
	"context"
	"testing"

	"github.com/getnvoi/nvoi/internal/testutil"
)

func TestManagedList_FiltersByKind(t *testing.T) {
	deploymentsJSON := `{"items": [
		{
			"metadata": {"name": "db", "labels": {"nvoi/managed-kind": "postgres"}},
			"spec": {"replicas": 1, "template": {"spec": {"containers": [{"image": "postgres:17"}]}}},
			"status": {"readyReplicas": 1}
		},
		{
			"metadata": {"name": "coder", "labels": {"nvoi/managed-kind": "claude"}},
			"spec": {"replicas": 1, "template": {"spec": {"containers": [{"image": "ghcr.io/getnvoi/nvoi-agent:latest"}]}}},
			"status": {"readyReplicas": 1}
		},
		{
			"metadata": {"name": "web", "labels": {}},
			"spec": {"replicas": 2, "template": {"spec": {"containers": [{"image": "nginx"}]}}},
			"status": {"readyReplicas": 2}
		}
	]}`
	stsJSON := `{"items": []}`

	// Test filtering by "postgres" — should return only db.
	ssh := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "get deployments -l nvoi/managed-kind=postgres", Result: testutil.MockResult{Output: []byte(`{"items": [
				{"metadata": {"name": "db", "labels": {"nvoi/managed-kind": "postgres"}},
				 "spec": {"replicas": 1, "template": {"spec": {"containers": [{"image": "postgres:17"}]}}},
				 "status": {"readyReplicas": 1}}
			]}`)}},
			{Prefix: "get statefulsets -l nvoi/managed-kind=postgres", Result: testutil.MockResult{Output: []byte(stsJSON)}},
		},
	}

	cluster := testCluster(ssh)
	services, err := ManagedList(context.Background(), ManagedListRequest{
		Cluster: cluster,
		Kind:    "postgres",
	})
	if err != nil {
		t.Fatalf("ManagedList() error = %v", err)
	}
	if len(services) != 1 {
		t.Fatalf("len(services) = %d, want 1", len(services))
	}
	if services[0].Name != "db" {
		t.Errorf("Name = %q, want db", services[0].Name)
	}
	if services[0].ManagedKind != "postgres" {
		t.Errorf("ManagedKind = %q, want postgres", services[0].ManagedKind)
	}

	// Test filtering by "claude" — should return only coder.
	ssh2 := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "get deployments -l nvoi/managed-kind=claude", Result: testutil.MockResult{Output: []byte(`{"items": [
				{"metadata": {"name": "coder", "labels": {"nvoi/managed-kind": "claude"}},
				 "spec": {"replicas": 1, "template": {"spec": {"containers": [{"image": "ghcr.io/getnvoi/nvoi-agent:latest"}]}}},
				 "status": {"readyReplicas": 1}}
			]}`)}},
			{Prefix: "get statefulsets -l nvoi/managed-kind=claude", Result: testutil.MockResult{Output: []byte(stsJSON)}},
		},
	}

	cluster2 := testCluster(ssh2)
	services2, err := ManagedList(context.Background(), ManagedListRequest{
		Cluster: cluster2,
		Kind:    "claude",
	})
	if err != nil {
		t.Fatalf("ManagedList() error = %v", err)
	}
	if len(services2) != 1 {
		t.Fatalf("len(services) = %d, want 1", len(services2))
	}
	if services2[0].Name != "coder" {
		t.Errorf("Name = %q, want coder", services2[0].Name)
	}

	// Test all managed — no kind filter.
	ssh3 := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "get deployments -l nvoi/managed-kind", Result: testutil.MockResult{Output: []byte(deploymentsJSON)}},
			{Prefix: "get statefulsets -l nvoi/managed-kind", Result: testutil.MockResult{Output: []byte(stsJSON)}},
		},
	}
	cluster3 := testCluster(ssh3)
	services3, err := ManagedList(context.Background(), ManagedListRequest{
		Cluster: cluster3,
		Kind:    "",
	})
	if err != nil {
		t.Fatalf("ManagedList() all error = %v", err)
	}
	// web has no managed-kind label, so only db and coder should appear.
	// But the kubectl label selector "nvoi/managed-kind" without =value returns
	// all pods that HAVE the label, regardless of value. So web (no label) is excluded.
	managed := 0
	for _, s := range services3 {
		if s.ManagedKind != "" {
			managed++
		}
	}
	if managed < 2 {
		t.Errorf("expected at least 2 managed services, got %d from %v", managed, services3)
	}
}

// testCluster is defined in secret_test.go — shared across pkg/core tests.
