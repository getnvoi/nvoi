package core

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/internal/testutil"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// volumeCluster registers a per-test Hetzner fake and returns a Cluster
// pointed at it. The fake is pre-seeded with master + pgdata volume.
func volumeCluster(t *testing.T, ssh *testutil.MockSSH) (*testutil.HetznerFake, Cluster) {
	hz := testutil.NewHetznerFake(t)
	hz.SeedServer("nvoi-myapp-prod-master", "1.2.3.4", "10.0.1.1")
	hz.SeedVolume("nvoi-myapp-prod-pgdata", 20, "nvoi-myapp-prod-master")
	provName := fmt.Sprintf("volume-test-%p", hz)
	hz.Register(provName)
	cl := Cluster{
		AppName: "myapp", Env: "prod",
		Provider: provName, Output: &testutil.MockOutput{},
		SSHFunc: func(ctx context.Context, addr string) (utils.SSHClient, error) {
			return ssh, nil
		},
	}
	return hz, cl
}

func TestVolumeSet_ListServersUsesLabels(t *testing.T) {
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
	hz, cl := volumeCluster(t, ssh)

	_, err := VolumeSet(context.Background(), VolumeSetRequest{
		Cluster: cl,
		Name:    "pgdata",
		Size:    20,
		Server:  "master",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// ListServers at the Hetzner HTTP level is GET /servers?…&label_selector=app=nvoi-myapp-prod
	// Assert the fake saw at least one such call.
	assertAppLabelOnListServers(t, hz)
}

func TestVolumeDelete_ListServersUsesLabels(t *testing.T) {
	ssh := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "umount", Result: testutil.MockResult{}},
			{Prefix: "mountpoint", Result: testutil.MockResult{Err: fmt.Errorf("not mounted")}},
		},
	}
	hz, cl := volumeCluster(t, ssh)

	err := VolumeDelete(context.Background(), VolumeDeleteRequest{
		Cluster: cl,
		Name:    "pgdata",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertAppLabelOnListServers(t, hz)
}

// assertAppLabelOnListServers verifies the real hetzner.Client issued at
// least one GET /servers with an app=nvoi-myapp-prod label selector, and
// never an unlabeled one (which would hit foreign servers on a shared
// account).
func assertAppLabelOnListServers(t *testing.T, hz *testutil.HetznerFake) {
	t.Helper()
	if hz.Count("list-servers:labeled=") == 0 {
		t.Errorf("expected at least one labeled ListServers call, got ops: %v", hz.All())
	}
	for _, op := range hz.All() {
		if op == "list-servers:unlabeled" {
			t.Errorf("unlabeled ListServers would leak to foreign servers")
		}
		if strings.HasPrefix(op, "list-servers:labeled=") {
			if !strings.Contains(op, "app=nvoi-myapp-prod") {
				t.Errorf("ListServers labels[app] missing/wrong, got: %q", op)
			}
		}
	}
}
