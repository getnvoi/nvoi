package reconcile

import (
	"context"
	"testing"
)

func TestServers_FreshDeploy(t *testing.T) {
	log := &opLog{}
	dc := convergeDC(log, convergeMock())
	n := testNames()
	cfg := &AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]ServerDef{
			"master": {Type: "cx23", Region: "fsn1", Role: "master"},
		},
	}

	if err := Servers(context.Background(), dc, nil, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !log.has("ensure-server:" + n.Server("master")) {
		t.Errorf("master not created: %v", log.all())
	}
}

func TestServers_AlreadyConverged(t *testing.T) {
	log := &opLog{}
	dc := convergeDC(log, convergeMock())
	n := testNames()
	cfg := &AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
	}
	live := &LiveState{Servers: []string{"master"}}

	if err := Servers(context.Background(), dc, live, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !log.has("ensure-server:" + n.Server("master")) {
		t.Error("ensure-server should still be called (idempotent)")
	}
	if log.count("delete-server:") != 0 {
		t.Errorf("no orphans to delete: %v", log.all())
	}
}

func TestServers_OrphanRemoved(t *testing.T) {
	log := &opLog{}
	dc := convergeDC(log, convergeMock())
	n := testNames()
	cfg := &AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
	}
	live := &LiveState{Servers: []string{"master", "old-worker"}}

	if err := Servers(context.Background(), dc, live, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !log.has("delete-server:" + n.Server("old-worker")) {
		t.Errorf("orphan not removed: %v", log.all())
	}
}

func TestServers_MixedState(t *testing.T) {
	log := &opLog{}
	dc := convergeDC(log, convergeMock())
	n := testNames()
	cfg := &AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]ServerDef{
			"master":   {Type: "cx23", Region: "fsn1", Role: "master"},
			"worker-1": {Type: "cx33", Region: "fsn1", Role: "worker"},
		},
	}
	live := &LiveState{Servers: []string{"master", "stale-worker"}}

	if err := Servers(context.Background(), dc, live, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !log.has("ensure-server:" + n.Server("worker-1")) {
		t.Error("missing worker-1 not added")
	}
	if !log.has("delete-server:" + n.Server("stale-worker")) {
		t.Error("orphan stale-worker not removed")
	}
}

func TestServers_ScaleUp(t *testing.T) {
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
	live := &LiveState{Servers: []string{"master", "worker-1"}}

	if err := Servers(context.Background(), dc, live, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !log.has("ensure-server:" + n.Server("worker-2")) {
		t.Error("worker-2 not added on scale-up")
	}
	if log.count("delete-server:") != 0 {
		t.Errorf("scale-up should not delete: %v", log.all())
	}
}

func TestServers_ScaleDown(t *testing.T) {
	log := &opLog{}
	dc := convergeDC(log, convergeMock())
	n := testNames()
	cfg := &AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
	}
	live := &LiveState{Servers: []string{"master", "worker-1", "worker-2"}}

	if err := Servers(context.Background(), dc, live, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !log.has("delete-server:" + n.Server("worker-1")) {
		t.Error("worker-1 not removed")
	}
	if !log.has("delete-server:" + n.Server("worker-2")) {
		t.Error("worker-2 not removed")
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
