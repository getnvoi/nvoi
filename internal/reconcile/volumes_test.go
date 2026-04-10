package reconcile

import (
	"context"
	"testing"
)

func TestVolumes_FreshDeploy(t *testing.T) {
	log := &opLog{}
	dc := convergeDC(log, convergeMock())
	n := testNames()
	cfg := &AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Volumes: map[string]VolumeDef{"pgdata": {Size: 20, Server: "master"}},
	}

	if err := Volumes(context.Background(), dc, nil, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !log.has("ensure-volume:" + n.Volume("pgdata")) {
		t.Errorf("volume not created: %v", log.all())
	}
}

func TestVolumes_OrphanRemoved(t *testing.T) {
	log := &opLog{}
	dc := convergeDC(log, convergeMock())
	n := testNames()
	cfg := &AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Volumes: map[string]VolumeDef{"pgdata": {Size: 20, Server: "master"}},
	}
	live := &LiveState{Volumes: []string{"pgdata", "old-cache"}}

	if err := Volumes(context.Background(), dc, live, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !log.has("delete-volume:" + n.Volume("old-cache")) {
		t.Errorf("orphan not removed: %v", log.all())
	}
}

func TestVolumes_AlreadyConverged(t *testing.T) {
	log := &opLog{}
	dc := convergeDC(log, convergeMock())
	n := testNames()
	cfg := &AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Volumes: map[string]VolumeDef{"pgdata": {Size: 20, Server: "master"}},
	}
	live := &LiveState{Volumes: []string{"pgdata"}}

	if err := Volumes(context.Background(), dc, live, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !log.has("ensure-volume:" + n.Volume("pgdata")) {
		t.Error("ensure should still be called (idempotent)")
	}
	if log.count("delete-volume:") != 0 {
		t.Errorf("no orphans: %v", log.all())
	}
}

func TestVolumes_MixedState(t *testing.T) {
	log := &opLog{}
	dc := convergeDC(log, convergeMock())
	n := testNames()
	cfg := &AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Volumes: map[string]VolumeDef{
			"pgdata": {Size: 20, Server: "master"},
			"redis":  {Size: 10, Server: "master"},
		},
	}
	live := &LiveState{Volumes: []string{"pgdata", "old-cache"}}

	if err := Volumes(context.Background(), dc, live, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !log.has("ensure-volume:" + n.Volume("redis")) {
		t.Error("missing redis not added")
	}
	if !log.has("delete-volume:" + n.Volume("old-cache")) {
		t.Error("orphan old-cache not removed")
	}
}
