package reconcile

import (
	"context"
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
)

func TestCrons_FreshDeploy(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Crons:   map[string]config.CronDef{"cleanup": {Image: "busybox", Schedule: "0 * * * *", Command: "echo hi"}},
	}

	if err := Crons(context.Background(), dc, nil, cfg, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCrons_OrphanRemoved(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Crons:   map[string]config.CronDef{"cleanup": {Image: "busybox", Schedule: "0 * * * *", Command: "echo hi"}},
	}
	live := &config.LiveState{Crons: []string{"cleanup", "old-job"}}

	if err := Crons(context.Background(), dc, live, cfg, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// old-job orphan deleted via KubeClient.DeleteCronByName.
}

func TestCrons_AlreadyConverged(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Crons:   map[string]config.CronDef{"cleanup": {Image: "busybox", Schedule: "0 * * * *", Command: "echo hi"}},
	}
	live := &config.LiveState{Crons: []string{"cleanup"}}

	if err := Crons(context.Background(), dc, live, cfg, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCrons_DatabaseBackupNotOrphaned(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Volumes:  map[string]config.VolumeDef{"pgdata": {Size: 20, Server: "master"}},
		Crons:    map[string]config.CronDef{"cleanup": {Image: "busybox", Schedule: "0 * * * *", Command: "echo hi"}},
		Database: map[string]config.DatabaseDef{"main": {Kind: "postgres", Image: "postgres:17", Volume: "pgdata"}},
	}
	cfg.Resolve()
	live := &config.LiveState{Crons: []string{"cleanup", "main-db-backup", "stale-job"}}

	if err := Crons(context.Background(), dc, live, cfg, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// main-db-backup protected, stale-job deleted. Function succeeds.
}

func TestCrons_MultipleDatabasesProtected(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Volumes: map[string]config.VolumeDef{
			"pgdata":    {Size: 20, Server: "master"},
			"analytics": {Size: 20, Server: "master"},
		},
		Database: map[string]config.DatabaseDef{
			"main":      {Kind: "postgres", Image: "postgres:17", Volume: "pgdata"},
			"analytics": {Kind: "postgres", Image: "postgres:17", Volume: "analytics"},
		},
	}
	cfg.Resolve()
	live := &config.LiveState{Crons: []string{"main-db-backup", "analytics-db-backup", "orphan-job"}}

	if err := Crons(context.Background(), dc, live, cfg, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
