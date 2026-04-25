package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/internal/testutil"
)

// TestPrepareNode_InstallsZFSUtilsWhenAbsent locks the install path:
// when dpkg-query reports the package missing, we run apt install. The
// subsequent zpool create runs against a fake that already reports
// the pool imported — this test focuses on the install branch.
func TestPrepareNode_InstallsZFSUtilsWhenAbsent(t *testing.T) {
	ssh := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			// dpkg-query: not installed → empty output.
			{Prefix: "dpkg-query -W", Result: testutil.MockResult{Output: []byte("")}},
			{Prefix: "sudo DEBIAN_FRONTEND=noninteractive", Result: testutil.MockResult{}},
			// zpool list: pool already exists → short-circuit zpool create.
			{Prefix: "zpool list", Result: testutil.MockResult{Output: []byte(zpoolName + "\n")}},
		},
	}
	if err := PrepareNode(context.Background(), ssh, 20); err != nil {
		t.Fatalf("PrepareNode: %v", err)
	}
	if !sshCalled(ssh, "apt-get install -y -q zfsutils-linux") {
		t.Error("apt install was not invoked when zfsutils was absent")
	}
}

// TestPrepareNode_IdempotentWhenEverythingReady asserts the fast path:
// zfsutils already installed AND pool already imported → no mutating
// SSH calls. Critical for re-running deploys.
func TestPrepareNode_IdempotentWhenEverythingReady(t *testing.T) {
	ssh := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "dpkg-query -W", Result: testutil.MockResult{Output: []byte("install ok installed")}},
			{Prefix: "zpool list", Result: testutil.MockResult{Output: []byte(zpoolName + "\n")}},
		},
	}
	if err := PrepareNode(context.Background(), ssh, 20); err != nil {
		t.Fatalf("PrepareNode: %v", err)
	}
	// No apt, no truncate, no zpool create when both checks pass.
	for _, mutating := range []string{"apt-get install", "truncate -s", "zpool create"} {
		if sshCalled(ssh, mutating) {
			t.Errorf("idempotent re-run triggered %q", mutating)
		}
	}
}

// TestPrepareNode_CreatesPoolWhenAbsent locks the zpool create path:
// zfsutils installed, pool missing → truncate image file + zpool create
// with the right flags (ashift=12 + compression=lz4).
func TestPrepareNode_CreatesPoolWhenAbsent(t *testing.T) {
	ssh := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "dpkg-query -W", Result: testutil.MockResult{Output: []byte("install ok installed")}},
			// zpool list: empty → pool missing.
			{Prefix: "zpool list", Result: testutil.MockResult{Output: []byte("")}},
			// df: 40 GiB root, 20 GiB available.
			{Prefix: "df --output=avail", Result: testutil.MockResult{Output: []byte("20\n")}},
			{Prefix: "sudo mkdir -p", Result: testutil.MockResult{}},
			{Prefix: "sudo sh -c 'test -f", Result: testutil.MockResult{}},
			{Prefix: "sudo zpool create", Result: testutil.MockResult{}},
		},
	}
	if err := PrepareNode(context.Background(), ssh, 5); err != nil {
		t.Fatalf("PrepareNode: %v", err)
	}
	if !sshCalled(ssh, "sudo zpool create -o ashift=12 -O compression=lz4 -m none "+zpoolName) {
		t.Error("zpool create with expected flags was not invoked")
	}
	if !sshCalled(ssh, "truncate -s") {
		t.Error("pool image file was not truncated")
	}
}

// TestPrepareNode_RejectsWhenRootTooSmall asserts the node-too-small
// guard: if the root FS can't give us sizeGiB + rootReserveGiB, the
// function fails with a clear message pointing at "use a bigger
// server." Without this check, zpool create would silently fill the
// disk and take k3s/etcd down with it.
func TestPrepareNode_RejectsWhenRootTooSmall(t *testing.T) {
	ssh := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "dpkg-query -W", Result: testutil.MockResult{Output: []byte("install ok installed")}},
			{Prefix: "zpool list", Result: testutil.MockResult{Output: []byte("")}},
			// Only 10 GiB free, want 50 + 8 reserve → reject.
			{Prefix: "df --output=avail", Result: testutil.MockResult{Output: []byte("10\n")}},
		},
	}
	err := PrepareNode(context.Background(), ssh, 50)
	if err == nil {
		t.Fatal("expected error when root FS is too small")
	}
	if !strings.Contains(err.Error(), "bigger server") {
		t.Errorf("error should suggest bigger server; got: %v", err)
	}
}

// TestPrepareNode_RequiresSSH asserts the precondition — nil SSH
// client means nothing can execute, so we fail fast instead of
// panicking deep in ssh.Run.
func TestPrepareNode_RequiresSSH(t *testing.T) {
	if err := PrepareNode(context.Background(), nil, 20); err == nil {
		t.Fatal("expected error when ssh is nil")
	}
}

// TestPrepareNode_PropagatesInstallErrors asserts that an apt failure
// surfaces with context. Silent failures here would result in zpool
// create running against a node where zfsutils isn't actually present.
func TestPrepareNode_PropagatesInstallErrors(t *testing.T) {
	ssh := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "dpkg-query -W", Result: testutil.MockResult{Output: []byte("")}},
			{Prefix: "sudo DEBIAN_FRONTEND=noninteractive", Result: testutil.MockResult{Err: errors.New("apt network failure")}},
		},
	}
	err := PrepareNode(context.Background(), ssh, 20)
	if err == nil {
		t.Fatal("expected error on apt failure")
	}
	if !strings.Contains(err.Error(), "apt") {
		t.Errorf("error should reference apt; got: %v", err)
	}
}

// sshCalled reports whether the mock recorded an SSH command
// containing the given substring.
func sshCalled(ssh *testutil.MockSSH, substr string) bool {
	for _, c := range ssh.Calls {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}
