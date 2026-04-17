package core

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/internal/testutil"
	"github.com/getnvoi/nvoi/pkg/infra"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

func TestFindMaster_Found(t *testing.T) {
	ctx := context.Background()
	names, err := utils.NewNames("myapp", "prod")
	if err != nil {
		t.Fatalf("NewNames: %v", err)
	}

	hz := testutil.NewHetznerFake(t)
	hz.SeedServer("nvoi-myapp-prod-master", "1.2.3.4", "10.0.1.1")
	hz.Register("find-master-found")
	prov, err := provider.ResolveCompute("find-master-found", map[string]string{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	got, err := FindMaster(ctx, prov, names)
	if err != nil {
		t.Fatalf("FindMaster: unexpected error: %v", err)
	}
	if got.Name != "nvoi-myapp-prod-master" {
		t.Errorf("Name = %q, want nvoi-myapp-prod-master", got.Name)
	}
	if got.IPv4 != "1.2.3.4" {
		t.Errorf("IPv4 = %q, want 1.2.3.4", got.IPv4)
	}
	if got.PrivateIP != "10.0.1.1" {
		t.Errorf("PrivateIP = %q, want 10.0.1.1", got.PrivateIP)
	}
}

func TestFindMaster_NotFound(t *testing.T) {
	ctx := context.Background()
	names, err := utils.NewNames("myapp", "prod")
	if err != nil {
		t.Fatalf("NewNames: %v", err)
	}

	hz := testutil.NewHetznerFake(t)
	hz.Register("find-master-notfound")
	prov, err := provider.ResolveCompute("find-master-notfound", map[string]string{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	_, err = FindMaster(ctx, prov, names)
	if err == nil {
		t.Fatal("FindMaster: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no master server found") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "no master server found")
	}
}

func computeSetCluster(sshErr error) Cluster {
	sshKey, _, _ := utils.GenerateEd25519Key()
	return Cluster{
		AppName:  "myapp",
		Env:      "prod",
		Provider: "cluster-test",
		SSHKey:   sshKey,
		Output:   &testutil.MockOutput{},
		SSHFunc: func(ctx context.Context, addr string) (utils.SSHClient, error) {
			if sshErr != nil {
				return nil, sshErr
			}
			return &testutil.MockSSH{}, nil
		},
	}
}

func TestComputeSet_HostKeyChanged_HardError(t *testing.T) {
	// Real error from infra/ssh.go includes guidance text.
	realErr := fmt.Errorf("%w for 1.2.3.4:22 — server was likely recreated.\nRun: nvoi known-hosts clear 1.2.3.4:22\nOr remove the entry from ~/.nvoi/known_hosts", infra.ErrHostKeyChanged)

	ctx := context.Background()
	_, err := ComputeSet(ctx, ComputeSetRequest{
		Cluster:    computeSetCluster(realErr),
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
		Cluster:    computeSetCluster(realErr),
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

// TestComputeSet_SilencesNoKnownHost verifies the "no known host" path
// from ClearKnownHost on a first deploy does NOT produce a warning. Only
// real failures (corrupt file, permission denied) should warn.
//
// Used to also verify the EnsureDocker SSH-reconnect dance, but Docker is
// no longer installed on the host (k3s ships its own containerd; the
// registry is a k8s Deployment in kube-system, see pkg/kube/registry.go).
// A single SSH session covers the whole ComputeSet now.
func TestComputeSet_SilencesNoKnownHostOnFirstDeploy(t *testing.T) {
	sshKey, _, _ := utils.GenerateEd25519Key()
	provName := "silences-no-known-host"
	hz := testutil.NewHetznerFake(t)
	hz.SeedServer("nvoi-myapp-prod-master", "1.2.3.4", "10.0.1.1")
	hz.SeedFirewall("nvoi-myapp-prod-master-fw")
	hz.SeedNetwork("nvoi-myapp-prod-net")
	hz.Register(provName)

	cluster := Cluster{
		AppName: "myapp", Env: "prod",
		Provider: provName, SSHKey: sshKey,
		Output: &testutil.MockOutput{},
		SSHFunc: func(ctx context.Context, addr string) (utils.SSHClient, error) {
			return &testutil.MockSSH{
				Prefixes: []testutil.MockPrefix{
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
			}, nil
		},
	}

	_, _ = ComputeSet(context.Background(), ComputeSetRequest{
		Cluster:    cluster,
		Name:       "master",
		ServerType: "cx21",
		Region:     "fsn1",
	})

	out := cluster.Output.(*testutil.MockOutput)
	for _, w := range out.Warnings {
		if strings.Contains(w, "clear known host") {
			t.Errorf("'no known host' error should be silenced, got warning: %s", w)
		}
	}
}

func TestMasterSSH_SetAfterComputeSet(t *testing.T) {
	// Verify that MasterSSH is NOT set by ComputeSet — it's set later by the reconcile loop.
	sshKey, _, _ := utils.GenerateEd25519Key()
	provName := "masterssh-test"
	hz := testutil.NewHetznerFake(t)
	hz.SeedServer("nvoi-myapp-prod-master", "1.2.3.4", "10.0.1.1")
	hz.SeedFirewall("nvoi-myapp-prod-master-fw")
	hz.SeedNetwork("nvoi-myapp-prod-net")
	hz.Register(provName)

	cluster := Cluster{
		AppName: "myapp", Env: "prod",
		Provider: provName, SSHKey: sshKey,
		Output: &testutil.MockOutput{},
		SSHFunc: func(ctx context.Context, addr string) (utils.SSHClient, error) {
			return &testutil.MockSSH{
				Prefixes: []testutil.MockPrefix{
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
			}, nil
		},
	}

	_, _ = ComputeSet(context.Background(), ComputeSetRequest{
		Cluster:    cluster,
		Name:       "master",
		ServerType: "cx21",
		Region:     "fsn1",
	})

	// MasterSSH must NOT be set by ComputeSet — reconcile.go sets it separately.
	if cluster.MasterSSH != nil {
		t.Error("ComputeSet should not set MasterSSH — that's the reconcile loop's job")
	}
}
