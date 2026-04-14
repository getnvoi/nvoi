package reconcile

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getnvoi/nvoi/internal/config"
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
	kube.SetTestTiming(time.Millisecond, time.Millisecond)
	dnsGracePeriod = time.Millisecond
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
	log     *opLog
	ListErr error // if set, ListServers returns this error
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
	if m.ListErr != nil {
		return nil, m.ListErr
	}
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

// ── DescribeLive tests ───────────────────────────────────────────────────────

func TestDescribeLive_ComputeListError_NotTreatedAsFirstDeploy(t *testing.T) {
	// If ComputeList fails (provider API down, bad credentials), DescribeLive
	// must NOT return (nil, nil) — that means "first deploy" and would cause
	// duplicate server creation. It must return an error.
	log := &opLog{}
	mock := &trackingMock{log: log, ListErr: fmt.Errorf("API unreachable")}
	activeMock = mock

	sshKey, _, _ := utils.GenerateEd25519Key()
	dc := &config.DeployContext{
		Cluster: app.Cluster{
			AppName: "myapp", Env: "prod",
			Provider: "test-reconcile", Credentials: map[string]string{},
			SSHKey: sshKey,
			Output: &testutil.MockOutput{},
			SSHFunc: func(ctx context.Context, addr string) (utils.SSHClient, error) {
				return nil, fmt.Errorf("no SSH")
			},
		},
	}

	live, err := DescribeLive(context.Background(), dc, &config.AppConfig{App: "myapp", Env: "prod"})
	if err == nil {
		t.Fatal("expected error when ComputeList fails, got nil — would be misinterpreted as first deploy")
	}
	if live != nil {
		t.Error("live state should be nil on error")
	}
}

func TestDescribeLive_FirstDeploy_NoServers(t *testing.T) {
	// When ComputeList succeeds with zero servers and Describe fails (no master),
	// that's a genuine first deploy — (nil, nil) is correct.
	log := &opLog{}
	mock := &trackingMock{log: log}
	mock.Servers = nil // no servers at provider
	activeMock = mock

	sshKey, _, _ := utils.GenerateEd25519Key()
	dc := &config.DeployContext{
		Cluster: app.Cluster{
			AppName: "myapp", Env: "prod",
			Provider: "test-reconcile", Credentials: map[string]string{},
			SSHKey: sshKey,
			Output: &testutil.MockOutput{},
			SSHFunc: func(ctx context.Context, addr string) (utils.SSHClient, error) {
				return nil, fmt.Errorf("no master")
			},
		},
	}

	live, err := DescribeLive(context.Background(), dc, &config.AppConfig{App: "myapp", Env: "prod"})
	if err != nil {
		t.Fatalf("first deploy should return nil error, got: %v", err)
	}
	if live != nil {
		t.Error("first deploy should return nil live state")
	}
}

func TestDescribeLive_ReturnsSortedLists(t *testing.T) {
	log := &opLog{}
	mock := &trackingMock{log: log}
	// Provider returns servers in reverse alphabetical order.
	mock.Servers = []*provider.Server{
		{ID: "3", Name: "nvoi-myapp-prod-worker-2", Status: "running", IPv4: "1.2.3.6", PrivateIP: "10.0.1.3"},
		{ID: "1", Name: "nvoi-myapp-prod-master", Status: "running", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"},
		{ID: "2", Name: "nvoi-myapp-prod-worker-1", Status: "running", IPv4: "1.2.3.5", PrivateIP: "10.0.1.2"},
	}
	activeMock = mock

	ssh := convergeMock()
	sshKey, _, _ := utils.GenerateEd25519Key()
	dc := &config.DeployContext{
		Cluster: app.Cluster{
			AppName: "myapp", Env: "prod",
			Provider: "test-reconcile", Credentials: map[string]string{},
			SSHKey:    sshKey,
			Output:    &testutil.MockOutput{},
			MasterSSH: ssh,
			SSHFunc: func(ctx context.Context, addr string) (utils.SSHClient, error) {
				return ssh, nil
			},
		},
	}

	live, err := DescribeLive(context.Background(), dc, &config.AppConfig{App: "myapp", Env: "prod"})
	if err != nil {
		t.Fatalf("DescribeLive: %v", err)
	}
	if live == nil {
		t.Fatal("expected non-nil live state")
	}

	// Servers must be sorted regardless of provider return order.
	for i := 1; i < len(live.Servers); i++ {
		if live.Servers[i] < live.Servers[i-1] {
			t.Errorf("servers not sorted: %v", live.Servers)
			break
		}
	}
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
			// WaitRollout: get pods ... -o json → returns ready pod list
			{Prefix: "get pods", Result: testutil.MockResult{Output: []byte(readyPodJSON)}},
			// Ingress: ACME cert check (acme.json parsed in Go) + HTTPS wait
			{Prefix: "kubectl -n kube-system exec", Result: testutil.MockResult{Output: []byte(`{"letsencrypt":{"Certificates":[{"domain":{"main":"myapp.com"},"certificate":"base64data"}]}}`)}},
			{Prefix: "curl -s", Result: testutil.MockResult{Output: []byte("'200'")}},
			{Prefix: "delete ingress", Result: testutil.MockResult{}},
			// EnsureTraefikACME — waitForTraefikReady polls deploy/traefik
			{Prefix: "get deploy traefik", Result: testutil.MockResult{Output: []byte("'1/1'")}},
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
			{Prefix: "command -v kubectl", Result: testutil.MockResult{Output: []byte("True")}},
			{Prefix: "curl -fs http://", Result: testutil.MockResult{}},
			{Prefix: "docker run -d --name nvoi-registry", Result: testutil.MockResult{}},
			{Prefix: "docker rm -f", Result: testutil.MockResult{}},
			// k3s jsonpath ready checks — KUBECONFIG must be before jsonpath (worker join needs IP)
			{Prefix: "KUBECONFIG", Result: testutil.MockResult{Output: []byte("True,10.0.1.1")}},
			{Prefix: "sudo k3s kubectl get nodes", Result: testutil.MockResult{Output: []byte("True")}},
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

func testDC(ssh *testutil.MockSSH) *config.DeployContext {
	sshKey, _, _ := utils.GenerateEd25519Key()
	return &config.DeployContext{
		Cluster: app.Cluster{
			AppName: "myapp", Env: "prod",
			Provider: "test-compute", Credentials: map[string]string{},
			SSHKey:    sshKey,
			Output:    &testutil.MockOutput{},
			MasterSSH: ssh,
			SSHFunc: func(ctx context.Context, addr string) (utils.SSHClient, error) {
				return ssh, nil
			},
		},
	}
}

func convergeDC(log *opLog, ssh *testutil.MockSSH) *config.DeployContext {
	mock := &trackingMock{log: log}
	mock.Servers = []*provider.Server{{
		ID: "1", Name: "nvoi-myapp-prod-master", Status: "running",
		IPv4: "1.2.3.4", PrivateIP: "10.0.1.1",
	}}
	activeMock = mock

	sshKey, _, _ := utils.GenerateEd25519Key()
	return &config.DeployContext{
		Cluster: app.Cluster{
			AppName: "myapp", Env: "prod",
			Provider: "test-reconcile", Credentials: map[string]string{},
			SSHKey:    sshKey,
			Output:    &testutil.MockOutput{},
			MasterSSH: ssh,
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

func validCfg() *config.AppConfig {
	return &config.AppConfig{
		App: "myapp", Env: "prod",
		Providers: config.ProvidersDef{Compute: "test-compute"},
		Servers:   map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services:  map[string]config.ServiceDef{"web": {Image: "nginx"}},
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
