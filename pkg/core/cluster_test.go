package core

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/getnvoi/nvoi/internal/testutil"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

func init() {
	provider.RegisterCompute("cache-test", provider.CredentialSchema{Name: "cache-test"}, func(creds map[string]string) provider.ComputeProvider {
		return &testutil.MockCompute{
			Servers: []*provider.Server{{
				ID: "1", Name: "nvoi-myapp-prod-master",
				IPv4: "1.2.3.4", PrivateIP: "10.0.1.1",
				Status: provider.ServerRunning,
			}},
		}
	})
}

func cacheCluster(sshFn func(ctx context.Context, addr string) (utils.SSHClient, error)) Cluster {
	return Cluster{
		AppName: "myapp", Env: "prod",
		Provider: "cache-test",
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

func TestSSHCache_ReturnsSameConnection(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "true", Result: testutil.MockResult{Output: []byte("")}},
		},
	}

	var connectCount int32
	c := cacheCluster(func(ctx context.Context, addr string) (utils.SSHClient, error) {
		atomic.AddInt32(&connectCount, 1)
		return mock, nil
	})
	c.EnableSSHCache()
	defer c.Close()

	ctx := context.Background()

	// First call — should connect
	ssh1, _, err := c.SSH(ctx)
	if err != nil {
		t.Fatalf("SSH() #1: %v", err)
	}

	// Second call — should reuse cached connection
	ssh2, _, err := c.SSH(ctx)
	if err != nil {
		t.Fatalf("SSH() #2: %v", err)
	}

	if atomic.LoadInt32(&connectCount) != 1 {
		t.Errorf("expected 1 connection, got %d", connectCount)
	}

	// Both should be borrowedSSH wrappers
	if _, ok := ssh1.(borrowedSSH); !ok {
		t.Error("ssh1 should be borrowedSSH")
	}
	if _, ok := ssh2.(borrowedSSH); !ok {
		t.Error("ssh2 should be borrowedSSH")
	}
}

func TestSSHCache_ReconnectsOnDeadConnection(t *testing.T) {
	callCount := 0
	deadMock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "true", Result: testutil.MockResult{Err: fmt.Errorf("broken pipe")}},
		},
	}
	aliveMock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "true", Result: testutil.MockResult{Output: []byte("")}},
		},
	}

	c := cacheCluster(func(ctx context.Context, addr string) (utils.SSHClient, error) {
		callCount++
		if callCount == 1 {
			return deadMock, nil
		}
		return aliveMock, nil
	})
	c.EnableSSHCache()
	defer c.Close()

	ctx := context.Background()

	// First call — caches the dead connection
	_, _, err := c.SSH(ctx)
	if err != nil {
		t.Fatalf("SSH() #1: %v", err)
	}

	// Second call — liveness probe fails, reconnects
	ssh2, _, err := c.SSH(ctx)
	if err != nil {
		t.Fatalf("SSH() #2: %v", err)
	}

	if callCount != 2 {
		t.Errorf("expected 2 connections (reconnect after dead), got %d", callCount)
	}
	// ssh2 should wrap aliveMock
	_ = ssh2
}

func TestSSHCache_DoubleCloseIsSafe(t *testing.T) {
	mock := &testutil.MockSSH{}
	c := cacheCluster(func(ctx context.Context, addr string) (utils.SSHClient, error) {
		return mock, nil
	})
	c.EnableSSHCache()

	// Close without connecting — should be safe
	if err := c.Close(); err != nil {
		t.Fatalf("Close() without connection: %v", err)
	}
	// Double close
	if err := c.Close(); err != nil {
		t.Fatalf("Double Close(): %v", err)
	}
}

func TestSSH_NoCacheConnectsFresh(t *testing.T) {
	var connectCount int32
	mock := &testutil.MockSSH{}

	c := cacheCluster(func(ctx context.Context, addr string) (utils.SSHClient, error) {
		atomic.AddInt32(&connectCount, 1)
		return mock, nil
	})
	// NOT calling EnableSSHCache — each call should connect fresh

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
		t.Errorf("expected 2 connections without cache, got %d", connectCount)
	}

	// Without caching, connections should NOT be borrowedSSH
	if _, ok := ssh1.(borrowedSSH); ok {
		t.Error("ssh1 should not be borrowedSSH without cache")
	}
	if _, ok := ssh2.(borrowedSSH); ok {
		t.Error("ssh2 should not be borrowedSSH without cache")
	}
}

func TestCloseWithoutCache(t *testing.T) {
	c := Cluster{}
	if err := c.Close(); err != nil {
		t.Fatalf("Close() without cache: %v", err)
	}
}
