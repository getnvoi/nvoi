package infra

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/getnvoi/nvoi/internal/testutil"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// countingSSH wraps MockSSH and fails the first N calls to a specific command.
type countingSSH struct {
	*testutil.MockSSH
	failCmd   string
	failCount int
	mu        sync.Mutex
	calls     map[string]*atomic.Int32
}

func (c *countingSSH) Run(ctx context.Context, cmd string) ([]byte, error) {
	if cmd == c.failCmd {
		c.mu.Lock()
		if c.calls == nil {
			c.calls = make(map[string]*atomic.Int32)
		}
		counter, ok := c.calls[cmd]
		if !ok {
			counter = &atomic.Int32{}
			c.calls[cmd] = counter
		}
		c.mu.Unlock()
		n := counter.Add(1)
		if int(n) <= c.failCount {
			return nil, fmt.Errorf("mock: failing call %d to %s", n, cmd)
		}
	}
	return c.MockSSH.Run(ctx, cmd)
}

func (c *countingSSH) RunStream(ctx context.Context, cmd string, stdout, stderr io.Writer) error {
	return c.MockSSH.RunStream(ctx, cmd, stdout, stderr)
}

func (c *countingSSH) Upload(ctx context.Context, local io.Reader, remotePath string, mode fs.FileMode) error {
	return c.MockSSH.Upload(ctx, local, remotePath, mode)
}

func (c *countingSSH) Stat(ctx context.Context, remotePath string) (*utils.RemoteFileInfo, error) {
	return c.MockSSH.Stat(ctx, remotePath)
}

func (c *countingSSH) DialTCP(ctx context.Context, remoteAddr string) (net.Conn, error) {
	return c.MockSSH.DialTCP(ctx, remoteAddr)
}

func (c *countingSSH) Close() error {
	return c.MockSSH.Close()
}

func TestInstallK3sMaster_AlreadyInstalled(t *testing.T) {
	mock := testutil.NewMockSSH(map[string]testutil.MockResult{})
	mock.Prefixes = []testutil.MockPrefix{
		// Idempotency check: kubectl exists + jsonpath returns True
		{Prefix: "command -v kubectl", Result: testutil.MockResult{Output: []byte("True")}},
	}

	var buf bytes.Buffer
	node := Node{PublicIP: "1.2.3.4", PrivateIP: "10.0.0.1"}
	err := InstallK3sMaster(context.Background(), mock, node, &buf)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if !strings.Contains(buf.String(), "k3s already installed") {
		t.Errorf("expected output to contain 'k3s already installed', got: %q", buf.String())
	}
}

func TestInstallK3sMaster_FreshInstall(t *testing.T) {
	privateIP := "10.0.0.1"
	publicIP := "1.2.3.4"

	kubeconfigCmd := fmt.Sprintf(
		`mkdir -p /home/%s/.kube && sudo cp %s /home/%s/.kube/config && sudo sed -i 's/127.0.0.1/%s/g' /home/%s/.kube/config && sudo chown -R %s:%s /home/%s/.kube && chmod 600 /home/%s/.kube/config`,
		utils.DefaultUser, utils.KubeconfigPath, utils.DefaultUser, privateIP, utils.DefaultUser,
		utils.DefaultUser, utils.DefaultUser, utils.DefaultUser, utils.DefaultUser,
	)

	mock := testutil.NewMockSSH(map[string]testutil.MockResult{
		// discover private interface
		fmt.Sprintf("ip -o -4 addr show | awk '/%s/{print $2}' | head -1", privateIP): {
			Output: []byte("eth1\n"),
		},
		// kubeconfig setup
		kubeconfigCmd: {},
	})

	mock.Prefixes = []testutil.MockPrefix{
		// Idempotency check: kubectl not installed
		{Prefix: "command -v kubectl", Result: testutil.MockResult{Err: fmt.Errorf("not installed")}},
		// k3s install via RunStream
		{Prefix: "curl -sfL https://get.k3s.io", Result: testutil.MockResult{Output: []byte("k3s installed\n")}},
		// Wait for ready: jsonpath returns True
		{Prefix: "KUBECONFIG", Result: testutil.MockResult{Output: []byte("True")}},
	}

	var buf bytes.Buffer
	node := Node{PublicIP: publicIP, PrivateIP: privateIP}
	err := InstallK3sMaster(context.Background(), mock, node, &buf)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
}

// (TestConfigureK3sRegistry_* removed — registry credentials no longer
// live on the host. They're a kubernetes.io/dockerconfigjson Secret in
// the app namespace, applied by internal/reconcile/registries.go.)

func TestDiscoverPrivateInterface(t *testing.T) {
	privateIP := "10.0.0.1"
	cmd := fmt.Sprintf("ip -o -4 addr show | awk '/%s/{print $2}' | head -1", privateIP)

	mock := testutil.NewMockSSH(map[string]testutil.MockResult{
		cmd: {Output: []byte("eth1\n")},
	})

	iface, err := discoverPrivateInterface(context.Background(), mock, privateIP)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if iface != "eth1" {
		t.Errorf("expected 'eth1', got: %q", iface)
	}
}

func TestDiscoverPrivateInterface_Fallback(t *testing.T) {
	privateIP := "10.0.0.1"
	primaryCmd := fmt.Sprintf("ip -o -4 addr show | awk '/%s/{print $2}' | head -1", privateIP)
	fallbackCmd := fmt.Sprintf("ip -4 addr show | grep '%s' -B2 | grep -oP '(?<=: )[^:@]+(?=:)' | tail -1", privateIP)

	mock := testutil.NewMockSSH(map[string]testutil.MockResult{
		primaryCmd:  {Output: []byte("")},
		fallbackCmd: {Output: []byte("enp7s0\n")},
	})

	iface, err := discoverPrivateInterface(context.Background(), mock, privateIP)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if iface != "enp7s0" {
		t.Errorf("expected 'enp7s0', got: %q", iface)
	}
}

func TestDiscoverPrivateInterface_NotFound(t *testing.T) {
	privateIP := "10.0.0.1"
	primaryCmd := fmt.Sprintf("ip -o -4 addr show | awk '/%s/{print $2}' | head -1", privateIP)
	fallbackCmd := fmt.Sprintf("ip -4 addr show | grep '%s' -B2 | grep -oP '(?<=: )[^:@]+(?=:)' | tail -1", privateIP)

	mock := testutil.NewMockSSH(map[string]testutil.MockResult{
		primaryCmd:  {Output: []byte("")},
		fallbackCmd: {Output: []byte("")},
	})

	_, err := discoverPrivateInterface(context.Background(), mock, privateIP)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no interface found") {
		t.Errorf("expected error containing 'no interface found', got: %q", err.Error())
	}
}
