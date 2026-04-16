package core

import (
	"context"
	"fmt"
	"testing"

	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/testutil"
	"github.com/getnvoi/nvoi/pkg/utils"
)

func volumeCluster(mock *testutil.MockCompute, ssh *testutil.MockSSH) Cluster {
	provName := fmt.Sprintf("volume-test-%p", mock)
	provider.RegisterCompute(provName, provider.CredentialSchema{Name: provName}, func(creds map[string]string) provider.ComputeProvider {
		return mock
	})
	return Cluster{
		AppName: "myapp", Env: "prod",
		Provider: provName, Output: &testutil.MockOutput{},
		SSHFunc: func(ctx context.Context, addr string) (utils.SSHClient, error) {
			return ssh, nil
		},
	}
}

func TestVolumeSet_ListServersUsesLabels(t *testing.T) {
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{
			{ID: "1", Name: "nvoi-myapp-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"},
		},
		Volumes: []*provider.Volume{
			{Name: "nvoi-myapp-prod-pgdata", Size: 20, DevicePath: "/dev/sda"},
		},
	}
	ssh := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "mountpoint", Result: testutil.MockResult{Output: []byte("mounted\n")}},
			{Prefix: "blkid", Result: testutil.MockResult{Output: []byte("/dev/sda: TYPE=\"xfs\"")}},
			{Prefix: "test -b", Result: testutil.MockResult{}},
			{Prefix: "sudo mount", Result: testutil.MockResult{}},
			{Prefix: "sudo mkdir", Result: testutil.MockResult{}},
			{Prefix: "xfs_growfs", Result: testutil.MockResult{}},
		},
	}

	_, err := VolumeSet(context.Background(), VolumeSetRequest{
		Cluster: volumeCluster(mock, ssh),
		Name:    "pgdata",
		Size:    20,
		Server:  "master",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// ListServers must be called with app labels, never nil.
	if len(mock.ListServersCalls) == 0 {
		t.Fatal("ListServers was never called")
	}
	for i, labels := range mock.ListServersCalls {
		if labels == nil {
			t.Errorf("ListServers call %d used nil labels — would list all servers on the account", i)
		}
		if labels["app"] != "nvoi-myapp-prod" {
			t.Errorf("ListServers call %d labels[app] = %q, want nvoi-myapp-prod", i, labels["app"])
		}
	}
}

func TestVolumeDelete_ListServersUsesLabels(t *testing.T) {
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{
			{ID: "1", Name: "nvoi-myapp-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"},
		},
		Volumes: []*provider.Volume{
			{ID: "v1", Name: "nvoi-myapp-prod-pgdata", Size: 20},
		},
	}
	ssh := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "umount", Result: testutil.MockResult{}},
			{Prefix: "mountpoint", Result: testutil.MockResult{Err: fmt.Errorf("not mounted")}},
		},
	}

	err := VolumeDelete(context.Background(), VolumeDeleteRequest{
		Cluster: volumeCluster(mock, ssh),
		Name:    "pgdata",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// ListServers must be called with app labels, never nil.
	found := false
	for i, labels := range mock.ListServersCalls {
		if labels == nil {
			t.Errorf("ListServers call %d used nil labels — would SSH into foreign servers", i)
		}
		if labels != nil && labels["app"] == "nvoi-myapp-prod" {
			found = true
		}
	}
	if !found {
		t.Error("ListServers never called with app labels")
	}
}
