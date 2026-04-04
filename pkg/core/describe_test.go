package core

import (
	"context"
	"testing"

	"github.com/getnvoi/nvoi/internal/testutil"
)

func TestDescribeNodes(t *testing.T) {
	nodesJSON := `{
		"items": [{
			"metadata": {
				"name": "nvoi-myapp-prod-master",
				"labels": {
					"nvoi-role": "master"
				}
			},
			"status": {
				"addresses": [
					{"type": "InternalIP", "address": "10.0.1.1"},
					{"type": "ExternalIP", "address": "1.2.3.4"}
				],
				"conditions": [
					{"type": "Ready", "status": "True"}
				]
			}
		}]
	}`

	ssh := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "get nodes", Result: testutil.MockResult{Output: []byte(nodesJSON)}},
		},
	}

	ctx := context.Background()
	nodes := describeNodes(ctx, ssh)

	if len(nodes) != 1 {
		t.Fatalf("len(nodes) = %d, want 1", len(nodes))
	}
	n := nodes[0]
	if n.Name != "nvoi-myapp-prod-master" {
		t.Errorf("Name = %q, want %q", n.Name, "nvoi-myapp-prod-master")
	}
	if n.Status != "Ready" {
		t.Errorf("Status = %q, want %q", n.Status, "Ready")
	}
	if n.IP != "10.0.1.1" {
		t.Errorf("IP = %q, want %q", n.IP, "10.0.1.1")
	}
	if n.Role != "master" {
		t.Errorf("Role = %q, want %q", n.Role, "master")
	}
}

func TestDescribeWorkloads(t *testing.T) {
	replicas := `{
		"items": [{
			"metadata": {
				"name": "web",
				"creationTimestamp": "2026-04-03T10:00:00Z"
			},
			"spec": {
				"replicas": 2,
				"template": {
					"spec": {
						"containers": [{"image": "nginx:latest"}]
					}
				}
			},
			"status": {
				"readyReplicas": 2
			}
		}]
	}`

	emptyList := `{"items": []}`

	ssh := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "get deployments", Result: testutil.MockResult{Output: []byte(replicas)}},
			{Prefix: "get statefulsets", Result: testutil.MockResult{Output: []byte(emptyList)}},
		},
	}

	ctx := context.Background()
	workloads := describeWorkloads(ctx, ssh, "nvoi-myapp-prod")

	if len(workloads) != 1 {
		t.Fatalf("len(workloads) = %d, want 1", len(workloads))
	}
	w := workloads[0]
	if w.Name != "web" {
		t.Errorf("Name = %q, want %q", w.Name, "web")
	}
	if w.Kind != "deployment" {
		t.Errorf("Kind = %q, want %q", w.Kind, "deployment")
	}
	if w.Ready != "2/2" {
		t.Errorf("Ready = %q, want %q", w.Ready, "2/2")
	}
	if w.Image != "nginx:latest" {
		t.Errorf("Image = %q, want %q", w.Image, "nginx:latest")
	}
}

func TestDescribePods(t *testing.T) {
	podsJSON := `{
		"items": [{
			"metadata": {
				"name": "web-abc123",
				"creationTimestamp": "2026-04-03T10:00:00Z"
			},
			"spec": {
				"nodeName": "nvoi-myapp-prod-master"
			},
			"status": {
				"phase": "Running",
				"containerStatuses": [{
					"ready": true,
					"restartCount": 0,
					"state": {}
				}]
			}
		}]
	}`

	ssh := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "get pods", Result: testutil.MockResult{Output: []byte(podsJSON)}},
		},
	}

	ctx := context.Background()
	pods := describePods(ctx, ssh, "nvoi-myapp-prod")

	if len(pods) != 1 {
		t.Fatalf("len(pods) = %d, want 1", len(pods))
	}
	p := pods[0]
	if p.Name != "web-abc123" {
		t.Errorf("Name = %q, want %q", p.Name, "web-abc123")
	}
	if p.Status != "Running" {
		t.Errorf("Status = %q, want %q", p.Status, "Running")
	}
	if p.Node != "nvoi-myapp-prod-master" {
		t.Errorf("Node = %q, want %q", p.Node, "nvoi-myapp-prod-master")
	}
	if p.Restarts != 0 {
		t.Errorf("Restarts = %d, want 0", p.Restarts)
	}
}

func TestDescribeServices(t *testing.T) {
	svcJSON := `{
		"items": [{
			"metadata": {
				"name": "web"
			},
			"spec": {
				"type": "ClusterIP",
				"clusterIP": "10.43.0.100",
				"ports": [{
					"port": 3000,
					"protocol": "TCP"
				}]
			}
		}]
	}`

	ssh := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "get services", Result: testutil.MockResult{Output: []byte(svcJSON)}},
		},
	}

	ctx := context.Background()
	services := describeServices(ctx, ssh, "nvoi-myapp-prod")

	if len(services) != 1 {
		t.Fatalf("len(services) = %d, want 1", len(services))
	}
	s := services[0]
	if s.Name != "web" {
		t.Errorf("Name = %q, want %q", s.Name, "web")
	}
	if s.Type != "ClusterIP" {
		t.Errorf("Type = %q, want %q", s.Type, "ClusterIP")
	}
	if s.ClusterIP != "10.43.0.100" {
		t.Errorf("ClusterIP = %q, want %q", s.ClusterIP, "10.43.0.100")
	}
	if s.Ports != "3000/TCP" {
		t.Errorf("Ports = %q, want %q", s.Ports, "3000/TCP")
	}
}
