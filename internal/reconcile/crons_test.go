package reconcile

import (
	"context"
	"testing"
)

func TestCrons_FreshDeploy(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Crons:   map[string]CronDef{"cleanup": {Image: "busybox", Schedule: "0 * * * *", Command: "echo hi"}},
	}

	if err := Crons(context.Background(), dc, nil, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !sshContains(ssh, "replace", "apply") {
		t.Errorf("expected kubectl apply/replace: %v", ssh.Calls)
	}
}

func TestCrons_OrphanRemoved(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Crons:   map[string]CronDef{"cleanup": {Image: "busybox", Schedule: "0 * * * *", Command: "echo hi"}},
	}
	live := &LiveState{Crons: []string{"cleanup", "old-job"}}

	if err := Crons(context.Background(), dc, live, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !sshCallMatches(ssh, "old-job", "delete") {
		t.Errorf("orphan old-job not deleted: %v", ssh.Calls)
	}
}

func TestCrons_AlreadyConverged(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Crons:   map[string]CronDef{"cleanup": {Image: "busybox", Schedule: "0 * * * *", Command: "echo hi"}},
	}
	live := &LiveState{Crons: []string{"cleanup"}}

	if err := Crons(context.Background(), dc, live, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sshCallMatches(ssh, "cleanup", "delete cronjob") {
		t.Error("converged cron should not be deleted")
	}
}
