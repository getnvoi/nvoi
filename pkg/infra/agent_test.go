package infra

import (
	"context"
	"fmt"
	"testing"

	"github.com/getnvoi/nvoi/pkg/testutil"
)

func TestInstallAgent_Success(t *testing.T) {
	ssh := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "command -v nvoi", Result: testutil.MockResult{Output: []byte("/usr/local/bin/nvoi")}},
			{Prefix: "sudo mkdir -p", Result: testutil.MockResult{}},
			{Prefix: "test -f", Result: testutil.MockResult{Err: fmt.Errorf("not found")}}, // token doesn't exist yet
			{Prefix: "sudo mv", Result: testutil.MockResult{}},
		},
	}

	err := InstallAgent(context.Background(), ssh, "myapp", "prod", []byte("app: myapp"), []byte("KEY=val"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have uploaded config, env, token, and systemd unit.
	if len(ssh.Uploads) < 3 {
		t.Fatalf("expected at least 3 uploads (config, env, token), got %d", len(ssh.Uploads))
	}
}

func TestInstallAgent_BinaryInstallFails(t *testing.T) {
	ssh := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "command -v nvoi", Result: testutil.MockResult{Err: fmt.Errorf("not installed")}},
			{Prefix: "curl", Result: testutil.MockResult{Err: fmt.Errorf("curl failed")}},
		},
	}

	err := InstallAgent(context.Background(), ssh, "myapp", "prod", []byte("app: myapp"), nil)
	if err == nil {
		t.Fatal("expected error when binary install fails")
	}
	if got := err.Error(); !contains(got, "install nvoi binary") {
		t.Fatalf("error = %q, want mention of binary install", got)
	}
}

func TestInstallAgent_SystemdEnableFails(t *testing.T) {
	ssh := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "command -v nvoi", Result: testutil.MockResult{Output: []byte("/usr/local/bin/nvoi")}},
			{Prefix: "sudo mkdir -p", Result: testutil.MockResult{}},
			{Prefix: "test -f", Result: testutil.MockResult{}}, // token exists
			{Prefix: "sudo mv", Result: testutil.MockResult{Err: fmt.Errorf("systemctl failed")}},
		},
	}

	err := InstallAgent(context.Background(), ssh, "myapp", "prod", []byte("app: myapp"), nil)
	if err == nil {
		t.Fatal("expected error when systemd enable fails")
	}
	if got := err.Error(); !contains(got, "enable agent service") {
		t.Fatalf("error = %q, want mention of agent service", got)
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
