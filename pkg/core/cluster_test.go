package core

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/getnvoi/nvoi/internal/testutil"
	"github.com/getnvoi/nvoi/pkg/utils"
)

func init() {
	// Shared "cluster-test" provider: single pre-seeded master. Tests across
	// the pkg/core suite inherit this via computeSetCluster / testCluster.
	// nil cleanup = process lifetime.
	hz := testutil.NewHetznerFake(nil)
	hz.SeedServer("nvoi-myapp-prod-master", "1.2.3.4", "10.0.1.1")
	hz.Register("cluster-test")
}

func clusterWithSSHFunc(sshFn func(ctx context.Context, addr string) (utils.SSHClient, error)) Cluster {
	return Cluster{
		AppName: "myapp", Env: "prod",
		Provider: "cluster-test",
		Output:   &testutil.MockOutput{},
		SSHFunc:  sshFn,
	}
}

func TestBorrowedSSH_CloseIsNoop(t *testing.T) {
	inner := &testutil.MockSSH{}
	b := borrowedSSH{inner}
	if err := b.Close(); err != nil {
		t.Fatalf("borrowedSSH.Close() should return nil, got %v", err)
	}
	if inner.Closed {
		t.Error("borrowedSSH.Close() should not close the inner connection")
	}
}

func TestSSH_NodeShellSet_ReturnsBorrowed(t *testing.T) {
	mock := &testutil.MockSSH{}
	c := clusterWithSSHFunc(nil)
	c.NodeShell = mock

	ctx := context.Background()
	ssh, names, err := c.SSH(ctx)
	if err != nil {
		t.Fatalf("SSH(): %v", err)
	}
	if names == nil {
		t.Fatal("expected names")
	}
	if _, ok := ssh.(borrowedSSH); !ok {
		t.Error("with NodeShell set, SSH() should return borrowedSSH")
	}
	// Close should be a no-op
	ssh.Close()
	if mock.Closed {
		t.Error("borrowedSSH.Close() should not close the shared connection")
	}
}

func TestSSH_NodeShellSet_NeverConnects(t *testing.T) {
	mock := &testutil.MockSSH{}
	var connectCount int32
	c := clusterWithSSHFunc(func(ctx context.Context, addr string) (utils.SSHClient, error) {
		atomic.AddInt32(&connectCount, 1)
		return &testutil.MockSSH{}, nil
	})
	c.NodeShell = mock

	ctx := context.Background()
	_, _, _ = c.SSH(ctx)
	_, _, _ = c.SSH(ctx)
	_, _, _ = c.SSH(ctx)

	if atomic.LoadInt32(&connectCount) != 0 {
		t.Errorf("with NodeShell set, connect should never be called, got %d", connectCount)
	}
}

// TestSSH_NoNodeShell_Errors locks the C10 contract: Cluster.SSH() no
// longer has an on-demand fallback. The CLI dispatch path is responsible
// for resolving the InfraProvider and populating NodeShell BEFORE calling
// SSH(); reconcile populates it during Deploy. Pre-#47 this would have
// silently dialed via SSHFunc — that's now an error so misuse surfaces.
func TestSSH_NoNodeShell_Errors(t *testing.T) {
	c := clusterWithSSHFunc(nil)
	// NodeShell intentionally nil.
	_, _, err := c.SSH(context.Background())
	if err == nil {
		t.Fatal("expected error when NodeShell is nil")
	}
	if !strings.Contains(err.Error(), "no node shell available") {
		t.Errorf("error should mention 'no node shell available', got: %v", err)
	}
}

func TestConnect_UsesSSHFunc(t *testing.T) {
	var dialedAddr string
	mock := &testutil.MockSSH{}
	c := clusterWithSSHFunc(func(ctx context.Context, addr string) (utils.SSHClient, error) {
		dialedAddr = addr
		return mock, nil
	})

	ctx := context.Background()
	ssh, err := c.Connect(ctx, "5.6.7.8:22")
	if err != nil {
		t.Fatalf("Connect(): %v", err)
	}
	defer ssh.Close()

	if dialedAddr != "5.6.7.8:22" {
		t.Errorf("expected dial to 5.6.7.8:22, got %q", dialedAddr)
	}
}
