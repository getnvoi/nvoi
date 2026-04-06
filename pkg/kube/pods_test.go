package kube

import (
	"context"
	"testing"

	"github.com/getnvoi/nvoi/internal/testutil"
)

func TestGetAllPods_AllReady(t *testing.T) {
	podsJSON := `{
		"items": [
			{
				"metadata": {"name": "web-abc"},
				"status": {
					"phase": "Running",
					"containerStatuses": [{"ready": true, "restartCount": 0, "state": {"running": {}}}]
				}
			},
			{
				"metadata": {"name": "db-0"},
				"status": {
					"phase": "Running",
					"containerStatuses": [{"ready": true, "restartCount": 0, "state": {"running": {}}}]
				}
			}
		]
	}`

	ssh := testutil.NewMockSSH(nil)
	ssh.Prefixes = []testutil.MockPrefix{
		{Prefix: "get pods", Result: testutil.MockResult{Output: []byte(podsJSON)}},
	}

	pods, err := GetAllPods(context.Background(), ssh, "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pods) != 2 {
		t.Fatalf("pods = %d, want 2", len(pods))
	}
	for _, p := range pods {
		if !p.Ready {
			t.Errorf("pod %s should be ready", p.Name)
		}
		if p.Status != "Running" {
			t.Errorf("pod %s status = %q, want Running", p.Name, p.Status)
		}
	}
}

func TestGetAllPods_MixedState(t *testing.T) {
	podsJSON := `{
		"items": [
			{
				"metadata": {"name": "web-abc"},
				"status": {
					"phase": "Running",
					"containerStatuses": [{"ready": true, "restartCount": 0, "state": {"running": {}}}]
				}
			},
			{
				"metadata": {"name": "jobs-def"},
				"status": {
					"phase": "Running",
					"containerStatuses": [{"ready": false, "restartCount": 3, "state": {"waiting": {"reason": "CrashLoopBackOff"}}}]
				}
			}
		]
	}`

	ssh := testutil.NewMockSSH(nil)
	ssh.Prefixes = []testutil.MockPrefix{
		{Prefix: "get pods", Result: testutil.MockResult{Output: []byte(podsJSON)}},
	}

	pods, err := GetAllPods(context.Background(), ssh, "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pods) != 2 {
		t.Fatalf("pods = %d, want 2", len(pods))
	}

	if !pods[0].Ready {
		t.Error("web should be ready")
	}
	if pods[1].Ready {
		t.Error("jobs should not be ready")
	}
	if pods[1].Status != "CrashLoopBackOff" {
		t.Errorf("jobs status = %q, want CrashLoopBackOff", pods[1].Status)
	}
}

func TestGetAllPods_Empty(t *testing.T) {
	ssh := testutil.NewMockSSH(nil)
	ssh.Prefixes = []testutil.MockPrefix{
		{Prefix: "get pods", Result: testutil.MockResult{Output: []byte(`{"items":[]}`)}},
	}

	pods, err := GetAllPods(context.Background(), ssh, "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pods) != 0 {
		t.Errorf("pods = %d, want 0", len(pods))
	}
}

func TestGetAllPods_ContainerCreating(t *testing.T) {
	podsJSON := `{
		"items": [
			{
				"metadata": {"name": "web-abc"},
				"status": {
					"phase": "Pending",
					"containerStatuses": [{"ready": false, "restartCount": 0, "state": {"waiting": {"reason": "ContainerCreating"}}}]
				}
			}
		]
	}`

	ssh := testutil.NewMockSSH(nil)
	ssh.Prefixes = []testutil.MockPrefix{
		{Prefix: "get pods", Result: testutil.MockResult{Output: []byte(podsJSON)}},
	}

	pods, err := GetAllPods(context.Background(), ssh, "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pods[0].Ready {
		t.Error("should not be ready")
	}
	if pods[0].Status != "ContainerCreating" {
		t.Errorf("status = %q, want ContainerCreating", pods[0].Status)
	}
}
