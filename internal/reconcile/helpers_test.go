package reconcile

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/internal/testutil"
	"github.com/getnvoi/nvoi/internal/testutil/kubefake"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// activeHetzner is the per-test Hetzner fake. Tests that need seeded state
// mutate this. It's rebound by convergeDC.
var activeHetzner *testutil.HetznerFake

// opLog is a thin view into a HetznerFake's OpLog so existing test
// assertions (log.has, log.count, log.all) keep working unchanged.
type opLog struct{ *testutil.OpLog }

func (l *opLog) has(op string) bool      { return l.OpLog.Has(op) }
func (l *opLog) count(prefix string) int { return l.OpLog.Count(prefix) }
func (l *opLog) all() []string           { return l.OpLog.All() }

func init() {
	kube.SetTestTiming(time.Millisecond, time.Millisecond)
	dnsGracePeriod = time.Millisecond

	// Default "test-compute" provider: a pre-seeded Hetzner fake with one
	// running master. Tests using testDC() / testDCWithKube() inherit this.
	// Tests that need different seeded state call convergeDC (re-registers).
	// nil cleanup = process lifetime, which is what we want for init-time.
	f := testutil.NewHetznerFake(nil)
	f.SeedServer("nvoi-myapp-prod-master", "1.2.3.4", "10.0.1.1")
	f.Register("test-compute")
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
			// k3s master install (ComputeSet) — Docker is no longer
			// installed on the host; the registry is now a k8s Deployment
			// in kube-system applied by the reconcile step.
			{Prefix: "command -v kubectl", Result: testutil.MockResult{Output: []byte("True")}},
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

// kubeFakes maps each test's DeployContext to its KubeFake so tests can
// assert against the typed clientset without threading kf through every
// helper signature. Tests use kfFor(dc) to retrieve.
var kubeFakes = make(map[*config.DeployContext]*kubefake.KubeFake)

func kfFor(dc *config.DeployContext) *kubefake.KubeFake {
	return kubeFakes[dc]
}

func testDC(ssh *testutil.MockSSH) *config.DeployContext {
	kf := kubefake.NewKubeFake()
	kf.AutoReadyPods()
	dc := testDCWithKube(ssh, kf)
	kubeFakes[dc] = kf
	return dc
}

// testDCWithKube wires NodeShell and MasterKube together for tests that need
// to inspect or pre-populate the kube tracker.
func testDCWithKube(ssh *testutil.MockSSH, kf *kubefake.KubeFake) *config.DeployContext {
	sshKey, _, _ := utils.GenerateEd25519Key()
	return &config.DeployContext{
		Cluster: app.Cluster{
			AppName: "myapp", Env: "prod",
			Provider: "test-compute", Credentials: map[string]string{},
			SSHKey:     sshKey,
			Output:     &testutil.MockOutput{},
			NodeShell:  ssh,
			MasterKube: kf.Client,
			SSHFunc: func(ctx context.Context, addr string) (utils.SSHClient, error) {
				return ssh, nil
			},
		},
	}
}

// convergeDC installs a per-test Hetzner fake (pre-seeded with one running
// master) and returns a DeployContext wired to it. `log` receives a view into
// the fake's OpLog — existing `log.has(...)` assertions work unchanged.
func convergeDC(log *opLog, ssh *testutil.MockSSH) *config.DeployContext {
	// nil cleanup — convergeDC doesn't receive *testing.T. The fake lives for
	// the test binary's lifetime (not per-test). Acceptable: only one active
	// fake at a time, and re-registration replaces the previous factory.
	fake := testutil.NewHetznerFake(nil)
	// Simulate the state that exists after ServersAdd has provisioned the
	// master: master server + per-role firewalls + network. Tests that need a
	// truly empty provider call activeHetzner.Reset() after convergeDC.
	fake.SeedServer("nvoi-myapp-prod-master", "1.2.3.4", "10.0.1.1")
	fake.SeedFirewall("nvoi-myapp-prod-master-fw")
	fake.SeedFirewall("nvoi-myapp-prod-worker-fw")
	fake.SeedNetwork("nvoi-myapp-prod-net")
	fake.Register("test-reconcile")
	activeHetzner = fake
	*log = opLog{OpLog: fake.OpLog}

	kf := kubefake.NewKubeFake()
	kf.AutoReadyPods()
	sshKey, _, _ := utils.GenerateEd25519Key()
	dc := &config.DeployContext{
		Cluster: app.Cluster{
			AppName: "myapp", Env: "prod",
			Provider: "test-reconcile", Credentials: map[string]string{},
			SSHKey:     sshKey,
			Output:     &testutil.MockOutput{},
			NodeShell:  ssh,
			MasterKube: kf.Client,
			SSHFunc: func(ctx context.Context, addr string) (utils.SSHClient, error) {
				return ssh, nil
			},
		},
	}
	kubeFakes[dc] = kf
	return dc
}

func testCreds(kvs ...string) provider.CredentialSource {
	m := make(map[string]string, len(kvs)/2)
	for i := 0; i+1 < len(kvs); i += 2 {
		m[kvs[i]] = kvs[i+1]
	}
	return provider.MapSource{M: m}
}

func testDCWithCreds(ssh *testutil.MockSSH, kvs ...string) *config.DeployContext {
	dc := testDC(ssh)
	dc.Creds = testCreds(kvs...)
	return dc
}

func testNames() *utils.Names {
	n, _ := utils.NewNames("myapp", "prod")
	return n
}

func validCfg() *config.AppConfig {
	return &config.AppConfig{
		App: "myapp", Env: "prod",
		Providers: config.ProvidersDef{Infra: "test-compute"},
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

// ── DescribeLive tests ───────────────────────────────────────────────────────

func TestDescribeLive_ComputeListError_NotTreatedAsFirstDeploy(t *testing.T) {
	// If ComputeList fails (provider API down, bad credentials), DescribeLive
	// must NOT return (nil, nil) — that means "first deploy" and would cause
	// duplicate server creation. It must return an error.
	fake := testutil.NewHetznerFake(t)
	fake.Register("test-reconcile-listerr")
	fake.FailListServers(fmt.Errorf("API unreachable"))

	sshKey, _, _ := utils.GenerateEd25519Key()
	dc := &config.DeployContext{
		Cluster: app.Cluster{
			AppName: "myapp", Env: "prod",
			Provider: "test-reconcile-listerr", Credentials: map[string]string{},
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
	fake := testutil.NewHetznerFake(t)
	fake.Register("test-reconcile-empty")

	sshKey, _, _ := utils.GenerateEd25519Key()
	dc := &config.DeployContext{
		Cluster: app.Cluster{
			AppName: "myapp", Env: "prod",
			Provider: "test-reconcile-empty", Credentials: map[string]string{},
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
	fake := testutil.NewHetznerFake(t)
	fake.Register("test-reconcile-sorted")
	// Seed in reverse alphabetical order — DescribeLive must still return sorted.
	fake.SeedServer("nvoi-myapp-prod-worker-2", "1.2.3.6", "10.0.1.3")
	fake.SeedServer("nvoi-myapp-prod-master", "1.2.3.4", "10.0.1.1")
	fake.SeedServer("nvoi-myapp-prod-worker-1", "1.2.3.5", "10.0.1.2")

	ssh := convergeMock()
	kf := kubefake.NewKubeFake()
	sshKey, _, _ := utils.GenerateEd25519Key()
	dc := &config.DeployContext{
		Cluster: app.Cluster{
			AppName: "myapp", Env: "prod",
			Provider: "test-reconcile-sorted", Credentials: map[string]string{},
			SSHKey:     sshKey,
			Output:     &testutil.MockOutput{},
			NodeShell:  ssh,
			MasterKube: kf.Client,
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
