package reconcile

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/internal/testutil"
	"github.com/getnvoi/nvoi/internal/testutil/kubefake"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
	// Register every production BuildProvider so ValidateConfig tests that
	// set cfg.Providers.Build resolve the real capability bits. Matches the
	// cmd/cli binary's blank-imports in main.go:
	//   - local   — RequiresBuilders: false (default)
	//   - ssh     — RequiresBuilders: true
	//   - daytona — RequiresBuilders: false (runs in a managed sandbox)
	_ "github.com/getnvoi/nvoi/pkg/provider/build/daytona"
	_ "github.com/getnvoi/nvoi/pkg/provider/build/local"
	_ "github.com/getnvoi/nvoi/pkg/provider/build/ssh"
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
			// ZFS prepare-node phase — postgres.PrepareNode runs over
			// the per-node SSH. Tests exercise the short-circuit paths
			// by default: dpkg-query reports zfsutils already
			// installed, zpool list reports the pool imported, so no
			// mutating commands fire. Tests that want to exercise the
			// install/create branches override their own MockSSH.
			//
			// MUST come before the "true" catch-all below: MockSSH
			// matches by Contains, and `zpool list ... || true` would
			// otherwise hit "true" first and short-circuit empty.
			{Prefix: "dpkg-query -W", Result: testutil.MockResult{Output: []byte("install ok installed")}},
			{Prefix: "zpool list", Result: testutil.MockResult{Output: []byte("nvoi-zfs\n")}},
			{Prefix: "sudo DEBIAN_FRONTEND", Result: testutil.MockResult{}},
			{Prefix: "df --output=avail", Result: testutil.MockResult{Output: []byte("100\n")}},
			{Prefix: "sudo zpool create", Result: testutil.MockResult{}},
			// OpenEBS ZFS-LocalPV CSI install — one-shot kubectl apply
			// on the master. Idempotent in prod; tests just need the
			// prefix to match so the command succeeds silently.
			{Prefix: "sudo k3s kubectl apply", Result: testutil.MockResult{}},
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

// DescribeLive tests deleted in D3 — DescribeLive itself no longer
// exists. Live-state lookup is now done per-step (Services / Crons
// query kube directly, TeardownOrphans calls infra.LiveSnapshot
// internally). The "first deploy", "API down", and "sorted" semantics
// are exercised by the per-step paths.
