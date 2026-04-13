package reconcile

import (
	"context"
	"fmt"
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/internal/testutil"
	"github.com/getnvoi/nvoi/pkg/provider"
)

func TestServersAdd_FreshDeploy(t *testing.T) {
	log := &opLog{}
	dc := convergeDC(log, convergeMock())
	n := testNames()
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{
			"master": {Type: "cx23", Region: "fsn1", Role: "master"},
		},
	}

	if err := ServersAdd(context.Background(), dc, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !log.has("ensure-server:" + n.Server("master")) {
		t.Errorf("master not created: %v", log.all())
	}
}

func TestServersAdd_AlreadyConverged(t *testing.T) {
	log := &opLog{}
	dc := convergeDC(log, convergeMock())
	n := testNames()
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
	}

	if err := ServersAdd(context.Background(), dc, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !log.has("ensure-server:" + n.Server("master")) {
		t.Error("ensure-server should still be called (idempotent)")
	}
	if log.count("delete-server:") != 0 {
		t.Errorf("ServersAdd should never delete: %v", log.all())
	}
}

func TestServersRemoveOrphans(t *testing.T) {
	log := &opLog{}
	dc := convergeDC(log, convergeMock())
	n := testNames()
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
	}
	live := &config.LiveState{Servers: []string{"master", "old-worker"}}
	// Orphan server exists at the provider
	activeMock.Servers = append(activeMock.Servers, &provider.Server{ID: "2", Name: n.Server("old-worker"), IPv4: "5.6.7.8"})

	if err := ServersRemoveOrphans(context.Background(), dc, live, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !log.has("delete-server:" + n.Server("old-worker")) {
		t.Errorf("orphan not removed: %v", log.all())
	}
}

func TestServersRemoveOrphans_NilLive(t *testing.T) {
	log := &opLog{}
	dc := convergeDC(log, convergeMock())
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
	}

	// nil live = first deploy, no orphans
	if err := ServersRemoveOrphans(context.Background(), dc, nil, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if log.count("delete-server:") != 0 {
		t.Errorf("nil live should not delete anything: %v", log.all())
	}
}

func TestServersAdd_ScaleUp(t *testing.T) {
	log := &opLog{}
	dc := convergeDC(log, convergeMock())
	n := testNames()
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{
			"master":   {Type: "cx23", Region: "fsn1", Role: "master"},
			"worker-1": {Type: "cx33", Region: "fsn1", Role: "worker"},
			"worker-2": {Type: "cx33", Region: "fsn1", Role: "worker"},
		},
	}

	if err := ServersAdd(context.Background(), dc, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !log.has("ensure-server:" + n.Server("worker-2")) {
		t.Error("worker-2 not added on scale-up")
	}
	if log.count("delete-server:") != 0 {
		t.Errorf("scale-up should not delete: %v", log.all())
	}
}

func TestServersRemoveOrphans_ScaleDown(t *testing.T) {
	log := &opLog{}
	dc := convergeDC(log, convergeMock())
	n := testNames()
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
	}
	live := &config.LiveState{Servers: []string{"master", "worker-1", "worker-2"}}
	// Orphan servers exist at the provider
	activeMock.Servers = append(activeMock.Servers,
		&provider.Server{ID: "2", Name: n.Server("worker-1"), IPv4: "5.6.7.8"},
		&provider.Server{ID: "3", Name: n.Server("worker-2"), IPv4: "9.10.11.12"},
	)

	if err := ServersRemoveOrphans(context.Background(), dc, live, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !log.has("delete-server:" + n.Server("worker-1")) {
		t.Error("worker-1 not removed")
	}
	if !log.has("delete-server:" + n.Server("worker-2")) {
		t.Error("worker-2 not removed")
	}
}

func TestServerReplacement_AddBeforeRemove(t *testing.T) {
	// Simulates replacing worker-1 with worker-2.
	// ServersAdd creates worker-2 first (no deletions).
	// Then services move workloads to worker-2.
	// Then ServersRemoveOrphans deletes worker-1.
	log := &opLog{}
	dc := convergeDC(log, convergeMock())
	n := testNames()

	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{
			"master":   {Type: "cx23", Region: "fsn1", Role: "master"},
			"worker-2": {Type: "cx33", Region: "fsn1", Role: "worker"},
		},
	}
	live := &config.LiveState{Servers: []string{"master", "worker-1"}}

	// Phase 1: add desired
	if err := ServersAdd(context.Background(), dc, cfg); err != nil {
		t.Fatalf("ServersAdd: %v", err)
	}
	if !log.has("ensure-server:" + n.Server("worker-2")) {
		t.Error("worker-2 not created")
	}
	if log.count("delete-server:") != 0 {
		t.Error("ServersAdd should not delete anything")
	}

	// (services would be reconciled here, moving workloads to worker-2)

	// Phase 2: remove orphans — orphan server exists at provider
	activeMock.Servers = append(activeMock.Servers, &provider.Server{ID: "4", Name: n.Server("worker-1"), IPv4: "5.6.7.8"})
	if err := ServersRemoveOrphans(context.Background(), dc, live, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !log.has("delete-server:" + n.Server("worker-1")) {
		t.Error("orphan worker-1 not removed")
	}
}

func TestServersRemoveOrphans_DrainFailOnReadyNode_BlocksDelete(t *testing.T) {
	ssh := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "create namespace", Result: testutil.MockResult{}},
			{Prefix: "apply", Result: testutil.MockResult{}},
			{Prefix: "replace", Result: testutil.MockResult{}},
			// Node exists and is Ready
			{Prefix: "get node", Result: testutil.MockResult{Output: []byte("nvoi-myapp-prod-old-worker   Ready")}},
			// Drain fails
			{Prefix: "drain", Result: testutil.MockResult{Err: fmt.Errorf("eviction timeout")}},
			// Ready check returns True — node is alive
			{Prefix: "jsonpath", Result: testutil.MockResult{Output: []byte("'True'")}},
		},
	}
	log := &opLog{}
	dc := convergeDC(log, ssh)
	n := testNames()
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
	}
	live := &config.LiveState{Servers: []string{"master", "old-worker"}}
	activeMock.Servers = append(activeMock.Servers, &provider.Server{ID: "2", Name: n.Server("old-worker"), IPv4: "5.6.7.8"})

	err := ServersRemoveOrphans(context.Background(), dc, live, cfg)
	if err == nil {
		t.Fatal("expected error when drain fails on Ready node")
	}
	if log.has("delete-server:" + n.Server("old-worker")) {
		t.Error("server should NOT be deleted when drain fails on Ready node")
	}
}

func TestServersRemoveOrphans_DrainFailOnNotReadyNode_ProceedsWithDelete(t *testing.T) {
	ssh := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "create namespace", Result: testutil.MockResult{}},
			{Prefix: "apply", Result: testutil.MockResult{}},
			{Prefix: "replace", Result: testutil.MockResult{}},
			// Node exists
			{Prefix: "get node", Result: testutil.MockResult{Output: []byte("nvoi-myapp-prod-old-worker   NotReady")}},
			// Drain fails (node unreachable)
			{Prefix: "drain", Result: testutil.MockResult{Err: fmt.Errorf("timeout")}},
			// Ready check returns False — node is dead
			{Prefix: "jsonpath", Result: testutil.MockResult{Output: []byte("'False'")}},
			// Force delete node succeeds
			{Prefix: "delete node", Result: testutil.MockResult{}},
			// ListServers for ComputeDelete existence check
			{Prefix: "get pods", Result: testutil.MockResult{Output: []byte(`{"items":[]}`)}},
			{Prefix: "delete deployment", Result: testutil.MockResult{}},
			{Prefix: "delete statefulset", Result: testutil.MockResult{}},
			{Prefix: "delete service/", Result: testutil.MockResult{}},
		},
	}
	log := &opLog{}
	dc := convergeDC(log, ssh)
	n := testNames()
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
	}
	live := &config.LiveState{Servers: []string{"master", "old-worker"}}
	activeMock.Servers = append(activeMock.Servers, &provider.Server{ID: "2", Name: n.Server("old-worker"), IPv4: "5.6.7.8"})

	err := ServersRemoveOrphans(context.Background(), dc, live, cfg)
	if err != nil {
		t.Fatalf("expected success for NotReady node, got: %v", err)
	}
	if !log.has("delete-server:" + n.Server("old-worker")) {
		t.Error("dead node should be deleted after force-remove")
	}
}

func TestServersRemoveOrphans_NodeNotInCluster_ProceedsWithDelete(t *testing.T) {
	ssh := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "create namespace", Result: testutil.MockResult{}},
			{Prefix: "apply", Result: testutil.MockResult{}},
			{Prefix: "replace", Result: testutil.MockResult{}},
			// Node doesn't exist in cluster
			{Prefix: "get node", Result: testutil.MockResult{Err: fmt.Errorf("not found")}},
			{Prefix: "get pods", Result: testutil.MockResult{Output: []byte(`{"items":[]}`)}},
			{Prefix: "delete deployment", Result: testutil.MockResult{}},
			{Prefix: "delete statefulset", Result: testutil.MockResult{}},
			{Prefix: "delete service/", Result: testutil.MockResult{}},
		},
	}
	log := &opLog{}
	dc := convergeDC(log, ssh)
	n := testNames()
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
	}
	live := &config.LiveState{Servers: []string{"master", "old-worker"}}
	activeMock.Servers = append(activeMock.Servers, &provider.Server{ID: "2", Name: n.Server("old-worker"), IPv4: "5.6.7.8"})

	err := ServersRemoveOrphans(context.Background(), dc, live, cfg)
	if err != nil {
		t.Fatalf("expected success when node not in cluster, got: %v", err)
	}
	if !log.has("delete-server:" + n.Server("old-worker")) {
		t.Error("server should be deleted when node not in cluster")
	}
}

func TestSplitServers_WorkersSorted(t *testing.T) {
	servers := map[string]config.ServerDef{
		"worker-z": {Role: "worker", Type: "cx33", Region: "fsn1"},
		"master":   {Role: "master", Type: "cx23", Region: "fsn1"},
		"worker-a": {Role: "worker", Type: "cx33", Region: "fsn1"},
	}
	masters, workers := SplitServers(servers)
	if len(masters) != 1 || masters[0].Name != "master" {
		t.Errorf("expected 1 master, got: %v", masters)
	}
	if len(workers) != 2 || workers[0].Name != "worker-a" || workers[1].Name != "worker-z" {
		t.Errorf("workers should be sorted: %v", workers)
	}
}
