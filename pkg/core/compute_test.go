package core

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/getnvoi/nvoi/pkg/infra"
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

func TestFindMaster_Found(t *testing.T) {
	ctx := context.Background()
	names, err := utils.NewNames("myapp", "prod")
	if err != nil {
		t.Fatalf("NewNames: %v", err)
	}

	want := &provider.Server{
		ID:        "123",
		Name:      "nvoi-myapp-prod-master",
		Status:    provider.ServerRunning,
		IPv4:      "1.2.3.4",
		PrivateIP: "10.0.1.1",
	}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{want},
	}

	got, err := FindMaster(ctx, mock, names)
	if err != nil {
		t.Fatalf("FindMaster: unexpected error: %v", err)
	}
	if got.Name != want.Name {
		t.Errorf("Name = %q, want %q", got.Name, want.Name)
	}
	if got.IPv4 != want.IPv4 {
		t.Errorf("IPv4 = %q, want %q", got.IPv4, want.IPv4)
	}
	if got.PrivateIP != want.PrivateIP {
		t.Errorf("PrivateIP = %q, want %q", got.PrivateIP, want.PrivateIP)
	}
}

func TestFindMaster_NotFound(t *testing.T) {
	ctx := context.Background()
	names, err := utils.NewNames("myapp", "prod")
	if err != nil {
		t.Fatalf("NewNames: %v", err)
	}

	mock := &testutil.MockCompute{
		Servers: []*provider.Server{},
	}

	_, err = FindMaster(ctx, mock, names)
	if err == nil {
		t.Fatal("FindMaster: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no master server found") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "no master server found")
	}
}

func computeSetCluster() Cluster {
	sshKey, _, _ := utils.GenerateEd25519Key()
	return Cluster{
		AppName:  "myapp",
		Env:      "prod",
		Provider: "cluster-test",
		SSHKey:   sshKey,
		MasterIP: "1.2.3.4",
	}
}

func computeSetConnect(sshErr error) ConnectSSH {
	return func(ctx context.Context, addr string) (utils.SSHClient, error) {
		if sshErr != nil {
			return nil, sshErr
		}
		return &testutil.MockSSH{}, nil
	}
}

func TestComputeSet_HostKeyChanged_HardError(t *testing.T) {
	// Real error from infra/ssh.go includes guidance text.
	realErr := fmt.Errorf("%w for 1.2.3.4:22 — server was likely recreated.\nRun: nvoi known-hosts clear 1.2.3.4:22\nOr remove the entry from ~/.nvoi/known_hosts", infra.ErrHostKeyChanged)

	ctx := context.Background()
	_, err := ComputeSet(ctx, ComputeSetRequest{
		Cluster:    computeSetCluster(),
		ConnectSSH: computeSetConnect(realErr),
		Name:       "master",
		ServerType: "cx21",
		Region:     "fsn1",
	})
	if err == nil {
		t.Fatal("expected error for host key changed")
	}
	msg := err.Error()
	if !strings.Contains(msg, "host key changed") {
		t.Errorf("should mention host key changed, got: %v", msg)
	}
	if !strings.Contains(msg, "server was likely recreated") {
		t.Errorf("should include guidance about recreated server, got: %v", msg)
	}
	if !strings.Contains(msg, "known-hosts clear") {
		t.Errorf("should include clear command guidance, got: %v", msg)
	}
}

func TestComputeSet_AuthFailed_HardError(t *testing.T) {
	realErr := fmt.Errorf("%w for 1.2.3.4:22 — server does not accept this key", infra.ErrAuthFailed)

	ctx := context.Background()
	_, err := ComputeSet(ctx, ComputeSetRequest{
		Cluster:    computeSetCluster(),
		ConnectSSH: computeSetConnect(realErr),
		Name:       "master",
		ServerType: "cx21",
		Region:     "fsn1",
	})
	if err == nil {
		t.Fatal("expected error for auth failed")
	}
	msg := err.Error()
	if !strings.Contains(msg, "authentication failed") {
		t.Errorf("should mention authentication failed, got: %v", msg)
	}
	if !strings.Contains(msg, "does not accept this key") {
		t.Errorf("should include key rejection guidance, got: %v", msg)
	}
}

func TestComputeSet_ReconnectsSSHAfterDocker(t *testing.T) {
	// Track SSH connections to verify reconnect after EnsureDocker.
	var connections []*testutil.MockSSH
	var mu sync.Mutex

	sshKey, _, _ := utils.GenerateEd25519Key()
	provName := "reconnect-test"
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{
			ID: "1", Name: "nvoi-myapp-prod-master", Status: provider.ServerRunning,
			IPv4: "1.2.3.4", PrivateIP: "10.0.1.1",
		}},
	}
	provider.RegisterCompute(provName, provider.CredentialSchema{Name: provName}, func(creds map[string]string) provider.ComputeProvider {
		return mock
	})

	out := &testutil.MockOutput{}
	cluster := Cluster{
		AppName: "myapp", Env: "prod",
		Provider: provName, SSHKey: sshKey,
		MasterIP: "1.2.3.4",
	}
	connectSSH := func(ctx context.Context, addr string) (utils.SSHClient, error) {
		ssh := &testutil.MockSSH{
			Prefixes: []testutil.MockPrefix{
				{Prefix: "docker info", Result: testutil.MockResult{}},
				{Prefix: "sudo", Result: testutil.MockResult{}},
				{Prefix: "curl", Result: testutil.MockResult{}},
				{Prefix: "command -v", Result: testutil.MockResult{}},
				{Prefix: "cloud-init", Result: testutil.MockResult{}},
				{Prefix: "swapon", Result: testutil.MockResult{Output: []byte("/swapfile")}},
				{Prefix: "kubectl", Result: testutil.MockResult{}},
				{Prefix: "k3s", Result: testutil.MockResult{}},
				{Prefix: "cat", Result: testutil.MockResult{}},
				{Prefix: "mkdir", Result: testutil.MockResult{}},
				{Prefix: "install", Result: testutil.MockResult{}},
				{Prefix: "systemctl", Result: testutil.MockResult{}},
			},
		}
		mu.Lock()
		connections = append(connections, ssh)
		mu.Unlock()
		return ssh, nil
	}

	_, _ = ComputeSet(context.Background(), ComputeSetRequest{
		Cluster: cluster, Output: out,
		ConnectSSH: connectSSH,
		Name:       "master",
		ServerType: "cx21",
		Region:     "fsn1",
	})

	mu.Lock()
	defer mu.Unlock()

	if len(connections) < 2 {
		t.Fatalf("expected at least 2 SSH connections (initial + reconnect after docker), got %d", len(connections))
	}

	// First connection should be closed (pre-docker session).
	if !connections[0].Closed {
		t.Error("first SSH connection should be closed after EnsureDocker")
	}

	// ClearKnownHost returns "no known host" on first deploy — must NOT
	// produce a warning. Only real failures (corrupt file, permission denied)
	// should warn.
	for _, w := range out.Warnings {
		if strings.Contains(w, "clear known host") {
			t.Errorf("'no known host' error should be silenced, got warning: %s", w)
		}
	}
}
