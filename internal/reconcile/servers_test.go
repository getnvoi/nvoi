package reconcile

import (
	"context"
	"testing"
)

func TestServersAdd_FreshDeploy(t *testing.T) {
	log := &opLog{}
	dc := convergeDC(log, convergeMock())
	n := testNames()
	cfg := &AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]ServerDef{
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
	cfg := &AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
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
	cfg := &AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
	}
	live := &LiveState{Servers: []string{"master", "old-worker"}}

	ServersRemoveOrphans(context.Background(), dc, live, cfg)

	if !log.has("delete-server:" + n.Server("old-worker")) {
		t.Errorf("orphan not removed: %v", log.all())
	}
}

func TestServersRemoveOrphans_NilLive(t *testing.T) {
	log := &opLog{}
	dc := convergeDC(log, convergeMock())
	cfg := &AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
	}

	// nil live = first deploy, no orphans
	ServersRemoveOrphans(context.Background(), dc, nil, cfg)

	if log.count("delete-server:") != 0 {
		t.Errorf("nil live should not delete anything: %v", log.all())
	}
}

func TestServersAdd_ScaleUp(t *testing.T) {
	log := &opLog{}
	dc := convergeDC(log, convergeMock())
	n := testNames()
	cfg := &AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]ServerDef{
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
	cfg := &AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
	}
	live := &LiveState{Servers: []string{"master", "worker-1", "worker-2"}}

	ServersRemoveOrphans(context.Background(), dc, live, cfg)

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

	cfg := &AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]ServerDef{
			"master":   {Type: "cx23", Region: "fsn1", Role: "master"},
			"worker-2": {Type: "cx33", Region: "fsn1", Role: "worker"},
		},
	}
	live := &LiveState{Servers: []string{"master", "worker-1"}}

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

	// Phase 2: remove orphans
	ServersRemoveOrphans(context.Background(), dc, live, cfg)
	if !log.has("delete-server:" + n.Server("worker-1")) {
		t.Error("orphan worker-1 not removed")
	}
}

func TestSplitServers_WorkersSorted(t *testing.T) {
	servers := map[string]ServerDef{
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
