package core

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/testutil"
	"github.com/getnvoi/nvoi/pkg/utils"
)

func init() {
	provider.RegisterCompute("cluster-test", provider.CredentialSchema{Name: "cluster-test"}, func(creds map[string]string) provider.ComputeProvider {
		return &testutil.MockCompute{
			Servers: []*provider.Server{{
				ID: "1", Name: "nvoi-myapp-prod-master",
				IPv4: "1.2.3.4", PrivateIP: "10.0.1.1",
				Status: provider.ServerRunning,
			}},
		}
	})
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

func TestSSH_MasterSSHSet_ReturnsBorrowed(t *testing.T) {
	mock := &testutil.MockSSH{}
	c := clusterWithSSHFunc(nil)
	c.MasterSSH = mock

	ctx := context.Background()
	ssh, names, err := c.SSH(ctx)
	if err != nil {
		t.Fatalf("SSH(): %v", err)
	}
	if names == nil {
		t.Fatal("expected names")
	}
	if _, ok := ssh.(borrowedSSH); !ok {
		t.Error("with MasterSSH set, SSH() should return borrowedSSH")
	}
	// Close should be a no-op
	ssh.Close()
	if mock.Closed {
		t.Error("borrowedSSH.Close() should not close the shared connection")
	}
}

func TestSSH_MasterSSHSet_NeverConnects(t *testing.T) {
	mock := &testutil.MockSSH{}
	var connectCount int32
	c := clusterWithSSHFunc(func(ctx context.Context, addr string) (utils.SSHClient, error) {
		atomic.AddInt32(&connectCount, 1)
		return &testutil.MockSSH{}, nil
	})
	c.MasterSSH = mock

	ctx := context.Background()
	_, _, _ = c.SSH(ctx)
	_, _, _ = c.SSH(ctx)
	_, _, _ = c.SSH(ctx)

	if atomic.LoadInt32(&connectCount) != 0 {
		t.Errorf("with MasterSSH set, connect should never be called, got %d", connectCount)
	}
}

func TestSSH_NoMasterSSH_ConnectsFresh(t *testing.T) {
	var connectCount int32
	mock := &testutil.MockSSH{}

	c := clusterWithSSHFunc(func(ctx context.Context, addr string) (utils.SSHClient, error) {
		atomic.AddInt32(&connectCount, 1)
		return mock, nil
	})

	ctx := context.Background()
	ssh1, _, err := c.SSH(ctx)
	if err != nil {
		t.Fatalf("SSH() #1: %v", err)
	}
	ssh2, _, err := c.SSH(ctx)
	if err != nil {
		t.Fatalf("SSH() #2: %v", err)
	}

	if atomic.LoadInt32(&connectCount) != 2 {
		t.Errorf("without MasterSSH, each call should connect fresh, got %d", connectCount)
	}

	// Without MasterSSH, connections should NOT be borrowedSSH
	if _, ok := ssh1.(borrowedSSH); ok {
		t.Error("ssh1 should not be borrowedSSH")
	}
	if _, ok := ssh2.(borrowedSSH); ok {
		t.Error("ssh2 should not be borrowedSSH")
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
