package reconcile

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/getnvoi/nvoi/internal/testutil"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
	"github.com/spf13/viper"
)

// ── Provider registration ─────────────────────────────────────────────────────

var activeMock *trackingMock

func init() {
	provider.RegisterCompute("test-compute", provider.CredentialSchema{Name: "test-compute"}, func(creds map[string]string) provider.ComputeProvider {
		return &testutil.MockCompute{
			Servers: []*provider.Server{{
				ID: "1", Name: "nvoi-myapp-prod-master", Status: "running",
				IPv4: "1.2.3.4", PrivateIP: "10.0.1.1",
			}},
		}
	})
	provider.RegisterCompute("test-reconcile", provider.CredentialSchema{Name: "test-reconcile"}, func(creds map[string]string) provider.ComputeProvider {
		return activeMock
	})
	kube.CaddyReloadDelay = 0
}

// ── Operation log ─────────────────────────────────────────────────────────────

type opLog struct {
	mu  sync.Mutex
	ops []string
}

func (l *opLog) record(op string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ops = append(l.ops, op)
}

func (l *opLog) has(op string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, o := range l.ops {
		if o == op {
			return true
		}
	}
	return false
}

func (l *opLog) count(prefix string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, o := range l.ops {
		if strings.HasPrefix(o, prefix) {
			n++
		}
	}
	return n
}

func (l *opLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	cp := make([]string, len(l.ops))
	copy(cp, l.ops)
	return cp
}

// ── Tracking compute mock ─────────────────────────────────────────────────────

type trackingMock struct {
	testutil.MockCompute
	log *opLog
}

func (m *trackingMock) EnsureServer(ctx context.Context, req provider.CreateServerRequest) (*provider.Server, error) {
	m.log.record("ensure-server:" + req.Name)
	return &provider.Server{
		ID: "1", Name: req.Name, Status: "running",
		IPv4: "1.2.3.4", PrivateIP: "10.0.1.1",
	}, nil
}

func (m *trackingMock) DeleteServer(ctx context.Context, req provider.DeleteServerRequest) error {
	m.log.record("delete-server:" + req.Name)
	return nil
}

func (m *trackingMock) ListServers(ctx context.Context, labels map[string]string) ([]*provider.Server, error) {
	return m.Servers, nil
}

func (m *trackingMock) EnsureVolume(ctx context.Context, req provider.CreateVolumeRequest) (*provider.Volume, error) {
	m.log.record("ensure-volume:" + req.Name)
	return &provider.Volume{Name: req.Name, Size: req.Size, DevicePath: "/dev/sda"}, nil
}

func (m *trackingMock) DeleteVolume(ctx context.Context, name string) error {
	m.log.record("delete-volume:" + name)
	return nil
}

func (m *trackingMock) DetachVolume(ctx context.Context, name string) error { return nil }

func (m *trackingMock) ListVolumes(ctx context.Context, labels map[string]string) ([]*provider.Volume, error) {
	return m.Volumes, nil
}

func (m *trackingMock) ReconcileFirewallRules(ctx context.Context, name string, allowed provider.PortAllowList) error {
	m.log.record("firewall:" + name)
	return nil
}

func (m *trackingMock) GetPrivateIP(ctx context.Context, serverID string) (string, error) {
	return "10.0.1.1", nil
}

// ── Shared test setup ─────────────────────────────────────────────────────────

// readyPodJSON is a kubectl get pods -o json response with one ready pod.
const readyPodJSON = `{"items":[{"metadata":{"name":"web-abc123"},"status":{"phase":"Running","conditions":[{"type":"Ready","status":"True"}],"containerStatuses":[{"name":"web","ready":true,"restartCount":0,"state":{"running":{"startedAt":"2025-01-01T00:00:00Z"}}}]}}]}`

func convergeMock() *testutil.MockSSH {
	return &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "create namespace", Result: testutil.MockResult{}},
			{Prefix: "apply --server-side", Result: testutil.MockResult{}},
			{Prefix: "replace", Result: testutil.MockResult{}},
			{Prefix: "get secret", Result: testutil.MockResult{Output: []byte("'{}'")}},
			{Prefix: "create secret", Result: testutil.MockResult{}},
			{Prefix: "patch secret", Result: testutil.MockResult{}},
			{Prefix: "delete deployment", Result: testutil.MockResult{}},
			{Prefix: "delete statefulset", Result: testutil.MockResult{}},
			{Prefix: "delete service/", Result: testutil.MockResult{}},
			{Prefix: "delete cronjob", Result: testutil.MockResult{}},
			{Prefix: "get service", Result: testutil.MockResult{Output: []byte("'80'")}},
			// FirstPod: get pods ... -o jsonpath → returns pod name
			{Prefix: "jsonpath='{.items[0].metadata.name}'", Result: testutil.MockResult{Output: []byte("'caddy-pod'")}},
			// WaitRollout: get pods ... -o json → returns ready pod list
			{Prefix: "get pods", Result: testutil.MockResult{Output: []byte(readyPodJSON)}},
			{Prefix: "caddy reload", Result: testutil.MockResult{}},
			{Prefix: "test -f", Result: testutil.MockResult{Output: []byte("ready")}},
			{Prefix: "curl -fsk", Result: testutil.MockResult{Output: []byte("'200'")}},
			{Prefix: "exec", Result: testutil.MockResult{}},
			{Prefix: "get deploy", Result: testutil.MockResult{Output: []byte(`{"items":[]}`)}},
			{Prefix: "get statefulset", Result: testutil.MockResult{Output: []byte(`{"items":[]}`)}},
			{Prefix: "get configmap", Result: testutil.MockResult{Output: []byte(`{"data":{}}`)}},
			{Prefix: "drain", Result: testutil.MockResult{}},
			{Prefix: "delete node", Result: testutil.MockResult{}},
			{Prefix: "cordon", Result: testutil.MockResult{}},
			{Prefix: "mkfs", Result: testutil.MockResult{}},
			{Prefix: "mkdir", Result: testutil.MockResult{}},
			{Prefix: "blkid", Result: testutil.MockResult{Output: []byte("/dev/sda: TYPE=\"xfs\"")}},
			{Prefix: "grep", Result: testutil.MockResult{}},
			{Prefix: "cat /etc/fstab", Result: testutil.MockResult{}},
			{Prefix: "umount", Result: testutil.MockResult{}},
			{Prefix: "sed", Result: testutil.MockResult{}},
			// Docker (ComputeSet)
			{Prefix: "sudo docker info", Result: testutil.MockResult{}},
			{Prefix: "sudo usermod", Result: testutil.MockResult{}},
			// k3s master install (ComputeSet)
			{Prefix: "command -v kubectl", Result: testutil.MockResult{}},
			{Prefix: "curl -fs http://", Result: testutil.MockResult{}},
			{Prefix: "docker run -d --name nvoi-registry", Result: testutil.MockResult{}},
			{Prefix: "docker rm -f", Result: testutil.MockResult{}},
			// k3s worker join (ComputeSet worker path)
			{Prefix: "sudo cat /var/lib/rancher/k3s/server/node-token", Result: testutil.MockResult{Output: []byte("test-token")}},
			{Prefix: "systemctl is-active", Result: testutil.MockResult{Err: fmt.Errorf("inactive")}},
			{Prefix: "ip -o -4 addr show", Result: testutil.MockResult{Output: []byte("eth1")}},
			{Prefix: "curl -sfL https://get.k3s.io", Result: testutil.MockResult{}},
			{Prefix: "KUBECONFIG=/home/deploy/.kube/config kubectl get nodes", Result: testutil.MockResult{Output: []byte("master Ready control-plane 1m v1.28\n10.0.1.1 Ready worker 1m v1.28")}},
			// Node labeling
			{Prefix: "label node", Result: testutil.MockResult{}},
			// Volume mount/unmount — mountpoint MUST be before "sudo mount" to avoid prefix collision
			{Prefix: "mountpoint", Result: testutil.MockResult{Output: []byte("mounted\n")}},
			{Prefix: "sudo mount", Result: testutil.MockResult{}},
			{Prefix: "test -b", Result: testutil.MockResult{Output: []byte("ready")}},
			{Prefix: "tee", Result: testutil.MockResult{}},
			{Prefix: "UUID=", Result: testutil.MockResult{}},
			{Prefix: "xfs_growfs", Result: testutil.MockResult{}},
			// Misc
			{Prefix: "sudo mkdir", Result: testutil.MockResult{}},
			{Prefix: "sudo systemctl", Result: testutil.MockResult{}},
			{Prefix: "cloud-init", Result: testutil.MockResult{}},
			{Prefix: "true", Result: testutil.MockResult{}},
		},
	}
}

func testDC(ssh *testutil.MockSSH) *DeployContext {
	sshKey, _, _ := utils.GenerateEd25519Key()
	return &DeployContext{
		Cluster: app.Cluster{
			AppName: "myapp", Env: "prod",
			Provider: "test-compute", Credentials: map[string]string{},
			SSHKey: sshKey,
			Output: &testutil.MockOutput{},
			SSHFunc: func(ctx context.Context, addr string) (utils.SSHClient, error) {
				return ssh, nil
			},
		},
	}
}

func convergeDC(log *opLog, ssh *testutil.MockSSH) *DeployContext {
	mock := &trackingMock{log: log}
	mock.Servers = []*provider.Server{{
		ID: "1", Name: "nvoi-myapp-prod-master", Status: "running",
		IPv4: "1.2.3.4", PrivateIP: "10.0.1.1",
	}}
	activeMock = mock

	sshKey, _, _ := utils.GenerateEd25519Key()
	return &DeployContext{
		Cluster: app.Cluster{
			AppName: "myapp", Env: "prod",
			Provider: "test-reconcile", Credentials: map[string]string{},
			SSHKey: sshKey,
			Output: &testutil.MockOutput{},
			SSHFunc: func(ctx context.Context, addr string) (utils.SSHClient, error) {
				return ssh, nil
			},
		},
	}
}

func testViper(kvs ...string) *viper.Viper {
	v := viper.New()
	for i := 0; i+1 < len(kvs); i += 2 {
		v.Set(kvs[i], kvs[i+1])
	}
	return v
}

func testNames() *utils.Names {
	n, _ := utils.NewNames("myapp", "prod")
	return n
}

func validCfg() *AppConfig {
	return &AppConfig{
		App: "myapp", Env: "prod",
		Providers: ProvidersDef{Compute: "test-compute"},
		Servers:   map[string]ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services:  map[string]ServiceDef{"web": {Image: "nginx"}},
	}
}

// ── SSH assertion helpers ─────────────────────────────────────────────────────

func sshContains(ssh *testutil.MockSSH, substrs ...string) bool {
	for _, call := range ssh.Calls {
		for _, s := range substrs {
			if strings.Contains(call, s) {
				return true
			}
		}
	}
	return false
}

func sshCallMatches(ssh *testutil.MockSSH, name, verb string) bool {
	for _, call := range ssh.Calls {
		if strings.Contains(call, name) {
			if verb == "" || strings.Contains(call, verb) {
				return true
			}
		}
	}
	return false
}

func uploadContains(ssh *testutil.MockSSH, substr string) bool {
	for _, u := range ssh.Uploads {
		if strings.Contains(string(u.Content), substr) {
			return true
		}
	}
	return false
}
