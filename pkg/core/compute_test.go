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
	ctx := context.Background()
	_, err := ComputeSet(ctx, ComputeSetRequest{
		Cluster:    computeSetCluster(fmt.Errorf("ssh dial: %w", infra.ErrHostKeyChanged)),
		Name:       "master",
		ServerType: "cx21",
		Region:     "fsn1",
	})
	if err == nil {
		t.Fatal("expected error for host key changed")
	}
	if !strings.Contains(err.Error(), "host key changed") {
		t.Errorf("error should mention host key changed, got: %v", err)
	}
}

func TestComputeSet_AuthFailed_HardError(t *testing.T) {
	ctx := context.Background()
	_, err := ComputeSet(ctx, ComputeSetRequest{
		Cluster:    computeSetCluster(fmt.Errorf("ssh dial: %w", infra.ErrAuthFailed)),
		Name:       "master",
		ServerType: "cx21",
		Region:     "fsn1",
	})
	if err == nil {
		t.Fatal("expected error for auth failed")
	}
	if !strings.Contains(err.Error(), "authentication failed") {
		t.Errorf("error should mention authentication failed, got: %v", err)
	}
}
