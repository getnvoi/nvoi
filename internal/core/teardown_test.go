package core

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/testutil"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// ── Tracking infrastructure ───────────────────────────────────────────────────

// opLog records every provider operation in order for assertions.
type opLog struct {
	mu     sync.Mutex
	ops    []string
	errors map[string]error
}

func newOpLog() *opLog {
	return &opLog{errors: map[string]error{}}
}

func (l *opLog) record(op string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ops = append(l.ops, op)
	if err, ok := l.errors[op]; ok {
		return err
	}
	return nil
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

func (l *opLog) indexOf(op string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i, o := range l.ops {
		if o == op {
			return i
		}
	}
	return -1
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

// ── Tracking compute provider ─────────────────────────────────────────────────

type trackingCompute struct {
	log       *opLog
	servers   []*provider.Server
	volumes   []*provider.Volume
	firewalls []*provider.Firewall
	networks  []*provider.Network
}

func (c *trackingCompute) ValidateCredentials(context.Context) error { return nil }
func (c *trackingCompute) ArchForType(string) string                 { return "amd64" }
func (c *trackingCompute) EnsureServer(context.Context, provider.CreateServerRequest) (*provider.Server, error) {
	return nil, nil
}
func (c *trackingCompute) DeleteServer(ctx context.Context, req provider.DeleteServerRequest) error {
	return c.log.record("delete-server:" + req.Name)
}
func (c *trackingCompute) ListServers(ctx context.Context, labels map[string]string) ([]*provider.Server, error) {
	return c.servers, nil
}
func (c *trackingCompute) DeleteFirewall(ctx context.Context, name string) error {
	return c.log.record("delete-firewall:" + name)
}
func (c *trackingCompute) DeleteNetwork(ctx context.Context, name string) error {
	return c.log.record("delete-network:" + name)
}
func (c *trackingCompute) ListAllFirewalls(context.Context) ([]*provider.Firewall, error) {
	return c.firewalls, nil
}
func (c *trackingCompute) ListAllNetworks(context.Context) ([]*provider.Network, error) {
	return c.networks, nil
}
func (c *trackingCompute) EnsureVolume(context.Context, provider.CreateVolumeRequest) (*provider.Volume, error) {
	return nil, nil
}
func (c *trackingCompute) ResizeVolume(context.Context, string, int) error { return nil }
func (c *trackingCompute) DetachVolume(ctx context.Context, name string) error {
	return c.log.record("detach-volume:" + name)
}
func (c *trackingCompute) DeleteVolume(ctx context.Context, name string) error {
	return c.log.record("delete-volume:" + name)
}
func (c *trackingCompute) ListVolumes(ctx context.Context, labels map[string]string) ([]*provider.Volume, error) {
	return c.volumes, nil
}
func (c *trackingCompute) GetPrivateIP(context.Context, string) (string, error) {
	return "10.0.0.1", nil
}
func (c *trackingCompute) ResolveDevicePath(vol *provider.Volume) string { return vol.DevicePath }
func (c *trackingCompute) ListResources(context.Context) ([]provider.ResourceGroup, error) {
	return nil, nil
}
func (c *trackingCompute) ReconcileFirewallRules(context.Context, string, provider.PortAllowList) error {
	return nil
}
func (c *trackingCompute) GetFirewallRules(context.Context, string) (provider.PortAllowList, error) {
	return nil, nil
}

var _ provider.ComputeProvider = (*trackingCompute)(nil)

// ── Tracking DNS provider ─────────────────────────────────────────────────────

type trackingDNS struct {
	log *opLog
}

func (d *trackingDNS) ValidateCredentials(context.Context) error { return nil }
func (d *trackingDNS) EnsureARecord(ctx context.Context, domain, ip string, proxied bool) error {
	return nil
}
func (d *trackingDNS) DeleteARecord(ctx context.Context, domain string) error {
	return d.log.record("delete-dns:" + domain)
}
func (d *trackingDNS) ListARecords(context.Context) ([]provider.DNSRecord, error) { return nil, nil }
func (d *trackingDNS) ListResources(context.Context) ([]provider.ResourceGroup, error) {
	return nil, nil
}

var _ provider.DNSProvider = (*trackingDNS)(nil)

// ── Tracking bucket provider ──────────────────────────────────────────────────

type trackingBucket struct {
	log *opLog
}

func (b *trackingBucket) ValidateCredentials(context.Context) error                 { return nil }
func (b *trackingBucket) EnsureBucket(context.Context, string) error                { return nil }
func (b *trackingBucket) SetCORS(context.Context, string, []string, []string) error { return nil }
func (b *trackingBucket) ClearCORS(context.Context, string) error                   { return nil }
func (b *trackingBucket) SetLifecycle(context.Context, string, int) error           { return nil }
func (b *trackingBucket) Credentials(context.Context) (provider.BucketCredentials, error) {
	return provider.BucketCredentials{}, nil
}
func (b *trackingBucket) ListResources(context.Context) ([]provider.ResourceGroup, error) {
	return nil, nil
}
func (b *trackingBucket) EmptyBucket(ctx context.Context, name string) error {
	return b.log.record("empty-bucket:" + name)
}
func (b *trackingBucket) DeleteBucket(ctx context.Context, name string) error {
	return b.log.record("delete-bucket:" + name)
}

var _ provider.BucketProvider = (*trackingBucket)(nil)

// ── Provider registration ─────────────────────────────────────────────────────

var (
	activeCompute *trackingCompute
	activeDNS     *trackingDNS
	activeBucket  *trackingBucket
)

func init() {
	provider.RegisterCompute("test-teardown", provider.CredentialSchema{
		Name: "test-teardown",
	}, func(creds map[string]string) provider.ComputeProvider {
		return activeCompute
	})
	provider.RegisterDNS("test-teardown-dns", provider.CredentialSchema{
		Name: "test-teardown-dns",
	}, func(creds map[string]string) provider.DNSProvider {
		return activeDNS
	})
	provider.RegisterBucket("test-teardown-bucket", provider.CredentialSchema{
		Name: "test-teardown-bucket",
	}, func(creds map[string]string) provider.BucketProvider {
		return activeBucket
	})
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func setupTeardown(log *opLog) *config.DeployContext {
	n := names()
	activeCompute = &trackingCompute{
		log: log,
		servers: []*provider.Server{
			{ID: "1", Name: n.Server("master"), IPv4: "1.2.3.4"},
			{ID: "2", Name: n.Server("worker"), IPv4: "5.6.7.8"},
		},
		volumes: []*provider.Volume{{ID: "1", Name: n.Volume("pgdata")}},
		firewalls: []*provider.Firewall{
			{ID: "1", Name: n.MasterFirewall()},
			{ID: "2", Name: n.WorkerFirewall()},
		},
		networks: []*provider.Network{{ID: "1", Name: n.Network()}},
	}
	activeDNS = &trackingDNS{log: log}
	activeBucket = &trackingBucket{log: log}

	sshKey, _, _ := utils.GenerateEd25519Key()

	return &config.DeployContext{
		Cluster: app.Cluster{
			AppName:  "myapp",
			Env:      "prod",
			Provider: "test-teardown",
			Output:   silentOutput{},
			SSHKey:   sshKey,
			SSHFunc: func(ctx context.Context, addr string) (utils.SSHClient, error) {
				return &testutil.MockSSH{}, nil
			},
		},
		DNS:     app.ProviderRef{Name: "test-teardown-dns"},
		Storage: app.ProviderRef{Name: "test-teardown-bucket"},
	}
}

func fullConfig() *config.AppConfig {
	cfg := &config.AppConfig{
		App: "myapp",
		Env: "prod",
		Servers: map[string]config.ServerDef{
			"master": {Type: "cx21", Region: "fsn1", Role: "master"},
			"worker": {Type: "cx21", Region: "fsn1", Role: "worker"},
		},
		Volumes: map[string]config.VolumeDef{
			"pgdata": {Size: 20, Server: "master"},
		},
		Secrets: []string{"DB_PASSWORD", "API_KEY"},
		Storage: map[string]config.StorageDef{
			"assets": {CORS: true},
		},
		Services: map[string]config.ServiceDef{
			"web": {Image: "nginx", Port: 80},
			"db":  {Image: "postgres:17", Port: 5432},
		},
		Crons: map[string]config.CronDef{
			"cleanup": {Image: "busybox", Schedule: "0 * * * *", Command: "echo hi"},
		},
		Domains: map[string][]string{
			"web": {"myapp.com", "www.myapp.com"},
		},
	}
	cfg.Resolve()
	return cfg
}

func names() *utils.Names {
	n, _ := utils.NewNames("myapp", "prod")
	return n
}

type silentOutput struct{}

func (silentOutput) Command(string, string, string, ...any) {}
func (silentOutput) Progress(string)                        {}
func (silentOutput) Success(string)                         {}
func (silentOutput) Warning(string)                         {}
func (silentOutput) Info(string)                            {}
func (silentOutput) Error(error)                            {}
func (silentOutput) Writer() io.Writer                      { return io.Discard }

// ── Tests: return value ───────────────────────────────────────────────────────

func TestTeardown_ReturnsNil(t *testing.T) {
	log := newOpLog()
	dc := setupTeardown(log)
	err := Teardown(context.Background(), dc, fullConfig(), false, false)
	if err != nil {
		t.Fatalf("teardown should return nil, got: %v", err)
	}
}

func TestTeardown_ReturnsNilWithAllFlags(t *testing.T) {
	log := newOpLog()
	dc := setupTeardown(log)
	err := Teardown(context.Background(), dc, fullConfig(), true, true)
	if err != nil {
		t.Fatalf("teardown with all flags should return nil, got: %v", err)
	}
}

// ── Tests: DNS ────────────────────────────────────────────────────────────────

func TestTeardown_DeletesDNSRecords(t *testing.T) {
	log := newOpLog()
	dc := setupTeardown(log)
	_ = Teardown(context.Background(), dc, fullConfig(), false, false)

	if !log.has("delete-dns:myapp.com") {
		t.Error("DNS record myapp.com not deleted")
	}
	if !log.has("delete-dns:www.myapp.com") {
		t.Error("DNS record www.myapp.com not deleted")
	}
	if log.count("delete-dns:") != 2 {
		t.Errorf("expected exactly 2 DNS deletes, got %d: %v", log.count("delete-dns:"), log.all())
	}
}

func TestTeardown_MultipleDomainServices(t *testing.T) {
	log := newOpLog()
	dc := setupTeardown(log)
	cfg := fullConfig()
	cfg.Services["api"] = config.ServiceDef{Image: "api:latest", Port: 8080}
	cfg.Domains["api"] = []string{"api.myapp.com"}

	_ = Teardown(context.Background(), dc, cfg, false, false)

	if !log.has("delete-dns:myapp.com") {
		t.Error("myapp.com not deleted")
	}
	if !log.has("delete-dns:www.myapp.com") {
		t.Error("www.myapp.com not deleted")
	}
	if !log.has("delete-dns:api.myapp.com") {
		t.Error("api.myapp.com not deleted")
	}
	if log.count("delete-dns:") != 3 {
		t.Errorf("expected 3 DNS deletes, got %d", log.count("delete-dns:"))
	}
}

func TestTeardown_NoDomains(t *testing.T) {
	log := newOpLog()
	dc := setupTeardown(log)
	cfg := fullConfig()
	cfg.Domains = nil

	_ = Teardown(context.Background(), dc, cfg, false, false)

	if log.count("delete-dns:") != 0 {
		t.Errorf("no domains configured, but DNS deletes occurred: %v", log.all())
	}
}

// ── Tests: servers ────────────────────────────────────────────────────────────

func TestTeardown_DeletesAllServers(t *testing.T) {
	log := newOpLog()
	dc := setupTeardown(log)
	n := names()
	_ = Teardown(context.Background(), dc, fullConfig(), false, false)

	if !log.has("delete-server:" + n.Server("worker")) {
		t.Errorf("worker not deleted")
	}
	if !log.has("delete-server:" + n.Server("master")) {
		t.Errorf("master not deleted")
	}
	if log.count("delete-server:") != 2 {
		t.Errorf("expected 2 server deletes, got %d", log.count("delete-server:"))
	}
}

func TestTeardown_WorkersBeforeMasters(t *testing.T) {
	log := newOpLog()
	dc := setupTeardown(log)
	n := names()
	_ = Teardown(context.Background(), dc, fullConfig(), false, false)

	workerIdx := log.indexOf("delete-server:" + n.Server("worker"))
	masterIdx := log.indexOf("delete-server:" + n.Server("master"))

	if workerIdx < 0 || masterIdx < 0 {
		t.Fatalf("missing server deletes: %v", log.all())
	}
	if workerIdx >= masterIdx {
		t.Errorf("worker at %d, master at %d — workers must be deleted first", workerIdx, masterIdx)
	}
}

func TestTeardown_MultipleWorkers(t *testing.T) {
	log := newOpLog()
	dc := setupTeardown(log)
	n := names()
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{
			"master":   {Type: "cx21", Region: "fsn1", Role: "master"},
			"worker-a": {Type: "cx21", Region: "fsn1", Role: "worker"},
			"worker-b": {Type: "cx33", Region: "fsn1", Role: "worker"},
		},
	}
	activeCompute.servers = []*provider.Server{
		{ID: "1", Name: n.Server("master")},
		{ID: "2", Name: n.Server("worker-a")},
		{ID: "3", Name: n.Server("worker-b")},
	}

	_ = Teardown(context.Background(), dc, cfg, false, false)

	wa := log.indexOf("delete-server:" + n.Server("worker-a"))
	wb := log.indexOf("delete-server:" + n.Server("worker-b"))
	m := log.indexOf("delete-server:" + n.Server("master"))

	if wa < 0 || wb < 0 || m < 0 {
		t.Fatalf("not all servers deleted: %v", log.all())
	}
	if wa >= m {
		t.Errorf("worker-a (%d) not before master (%d)", wa, m)
	}
	if wb >= m {
		t.Errorf("worker-b (%d) not before master (%d)", wb, m)
	}
}

func TestTeardown_MasterOnly(t *testing.T) {
	log := newOpLog()
	dc := setupTeardown(log)
	n := names()
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{
			"master": {Type: "cx21", Region: "fsn1", Role: "master"},
		},
	}

	_ = Teardown(context.Background(), dc, cfg, false, false)

	if !log.has("delete-server:" + n.Server("master")) {
		t.Error("master not deleted")
	}
	if log.count("delete-server:") != 1 {
		t.Errorf("expected 1 server delete, got %d", log.count("delete-server:"))
	}
}

// ── Tests: firewall + network ─────────────────────────────────────────────────

func TestTeardown_FirewallAndNetworkAlwaysDeleted(t *testing.T) {
	log := newOpLog()
	dc := setupTeardown(log)
	n := names()
	_ = Teardown(context.Background(), dc, fullConfig(), false, false)

	if !log.has("delete-firewall:" + n.MasterFirewall()) {
		t.Errorf("firewall %q not deleted", n.MasterFirewall())
	}
	if !log.has("delete-network:" + n.Network()) {
		t.Errorf("network %q not deleted", n.Network())
	}
}

func TestTeardown_FirewallAndNetworkAfterAllServers(t *testing.T) {
	log := newOpLog()
	dc := setupTeardown(log)
	n := names()
	_ = Teardown(context.Background(), dc, fullConfig(), false, false)

	masterIdx := log.indexOf("delete-server:" + n.Server("master"))
	fwIdx := log.indexOf("delete-firewall:" + n.MasterFirewall())
	netIdx := log.indexOf("delete-network:" + n.Network())

	if fwIdx <= masterIdx {
		t.Errorf("firewall (%d) before master (%d)", fwIdx, masterIdx)
	}
	if netIdx <= masterIdx {
		t.Errorf("network (%d) before master (%d)", netIdx, masterIdx)
	}
}

func TestTeardown_FirewallErrorDoesNotBlockNetwork(t *testing.T) {
	log := newOpLog()
	dc := setupTeardown(log)
	n := names()

	log.errors["delete-firewall:"+n.MasterFirewall()] = fmt.Errorf("firewall stuck")

	_ = Teardown(context.Background(), dc, fullConfig(), false, false)

	if !log.has("delete-firewall:" + n.MasterFirewall()) {
		t.Error("firewall delete not attempted")
	}
	if !log.has("delete-network:" + n.Network()) {
		t.Error("network not deleted after firewall error")
	}
}

func TestTeardown_EmptyConfig_StillDeletesFirewallAndNetwork(t *testing.T) {
	log := newOpLog()
	dc := setupTeardown(log)
	n := names()
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{},
	}

	_ = Teardown(context.Background(), dc, cfg, true, true)

	if !log.has("delete-firewall:" + n.MasterFirewall()) {
		t.Error("firewall not deleted on empty config")
	}
	if !log.has("delete-network:" + n.Network()) {
		t.Error("network not deleted on empty config")
	}
	if log.count("delete-server:") != 0 {
		t.Error("server deletes on empty config")
	}
}

// ── Tests: volumes ────────────────────────────────────────────────────────────

func TestTeardown_VolumesPreservedByDefault(t *testing.T) {
	log := newOpLog()
	dc := setupTeardown(log)
	_ = Teardown(context.Background(), dc, fullConfig(), false, false)

	if log.count("delete-volume:") != 0 {
		t.Errorf("volumes preserved by default, but got deletes: %v", log.all())
	}
}

func TestTeardown_VolumesDeletedWithFlag(t *testing.T) {
	log := newOpLog()
	dc := setupTeardown(log)
	n := names()
	_ = Teardown(context.Background(), dc, fullConfig(), true, false)

	if !log.has("delete-volume:" + n.Volume("pgdata")) {
		t.Errorf("volume %q not deleted with --delete-volumes", n.Volume("pgdata"))
	}
}

func TestTeardown_MultipleVolumes(t *testing.T) {
	log := newOpLog()
	dc := setupTeardown(log)
	n := names()
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{
			"master": {Type: "cx21", Region: "fsn1", Role: "master"},
		},
		Volumes: map[string]config.VolumeDef{
			"pgdata": {Size: 20, Server: "master"},
			"redis":  {Size: 10, Server: "master"},
		},
	}
	activeCompute.volumes = []*provider.Volume{
		{ID: "1", Name: n.Volume("pgdata")},
		{ID: "2", Name: n.Volume("redis")},
	}

	_ = Teardown(context.Background(), dc, cfg, true, false)

	if !log.has("delete-volume:" + n.Volume("pgdata")) {
		t.Error("pgdata not deleted")
	}
	if !log.has("delete-volume:" + n.Volume("redis")) {
		t.Error("redis not deleted")
	}
	if log.count("delete-volume:") != 2 {
		t.Errorf("expected 2 volume deletes, got %d", log.count("delete-volume:"))
	}
}

func TestTeardown_VolumesBeforeServers(t *testing.T) {
	log := newOpLog()
	dc := setupTeardown(log)
	n := names()
	_ = Teardown(context.Background(), dc, fullConfig(), true, false)

	volIdx := log.indexOf("delete-volume:" + n.Volume("pgdata"))
	srvIdx := log.indexOf("delete-server:" + n.Server("worker"))

	if volIdx < 0 || srvIdx < 0 {
		t.Fatalf("missing ops: %v", log.all())
	}
	if volIdx >= srvIdx {
		t.Errorf("volume (%d) not before servers (%d)", volIdx, srvIdx)
	}
}

// ── Tests: storage ────────────────────────────────────────────────────────────

func TestTeardown_StoragePreservedByDefault(t *testing.T) {
	log := newOpLog()
	dc := setupTeardown(log)
	_ = Teardown(context.Background(), dc, fullConfig(), false, false)

	if log.count("empty-bucket:") != 0 {
		t.Errorf("storage preserved by default, but got empties: %v", log.all())
	}
	if log.count("delete-bucket:") != 0 {
		t.Errorf("storage preserved by default, but got deletes: %v", log.all())
	}
}

func TestTeardown_StorageDeletedWithFlag(t *testing.T) {
	log := newOpLog()
	dc := setupTeardown(log)
	n := names()
	_ = Teardown(context.Background(), dc, fullConfig(), false, true)

	bucketName := n.Bucket("assets")
	if !log.has("empty-bucket:" + bucketName) {
		t.Errorf("bucket %q not emptied", bucketName)
	}
	if !log.has("delete-bucket:" + bucketName) {
		t.Errorf("bucket %q not deleted", bucketName)
	}
}

func TestTeardown_StorageEmptiedBeforeDeleted(t *testing.T) {
	log := newOpLog()
	dc := setupTeardown(log)
	n := names()
	_ = Teardown(context.Background(), dc, fullConfig(), false, true)

	bucketName := n.Bucket("assets")
	emptyIdx := log.indexOf("empty-bucket:" + bucketName)
	deleteIdx := log.indexOf("delete-bucket:" + bucketName)

	if emptyIdx < 0 || deleteIdx < 0 {
		t.Fatalf("missing bucket ops: %v", log.all())
	}
	if emptyIdx >= deleteIdx {
		t.Errorf("empty (%d) not before delete (%d)", emptyIdx, deleteIdx)
	}
}

func TestTeardown_MultipleStorageBuckets(t *testing.T) {
	log := newOpLog()
	dc := setupTeardown(log)
	n := names()
	cfg := fullConfig()
	cfg.Storage["uploads"] = config.StorageDef{}

	_ = Teardown(context.Background(), dc, cfg, false, true)

	if !log.has("delete-bucket:" + n.Bucket("assets")) {
		t.Error("assets bucket not deleted")
	}
	if !log.has("delete-bucket:" + n.Bucket("uploads")) {
		t.Error("uploads bucket not deleted")
	}
}

func TestTeardown_StorageBeforeServers(t *testing.T) {
	log := newOpLog()
	dc := setupTeardown(log)
	n := names()
	_ = Teardown(context.Background(), dc, fullConfig(), false, true)

	bucketIdx := log.indexOf("delete-bucket:" + n.Bucket("assets"))
	srvIdx := log.indexOf("delete-server:" + n.Server("worker"))

	if bucketIdx < 0 || srvIdx < 0 {
		t.Fatalf("missing ops: %v", log.all())
	}
	if bucketIdx >= srvIdx {
		t.Errorf("storage (%d) not before servers (%d)", bucketIdx, srvIdx)
	}
}

// ── Tests: error handling ─────────────────────────────────────────────────────

func TestTeardown_ErrorsNeverPropagate(t *testing.T) {
	log := newOpLog()
	dc := setupTeardown(log)
	n := names()

	// Inject errors everywhere
	log.errors["delete-dns:myapp.com"] = fmt.Errorf("dns timeout")
	log.errors["delete-server:"+n.Server("worker")] = fmt.Errorf("api 500")
	log.errors["delete-firewall:"+n.MasterFirewall()] = fmt.Errorf("firewall locked")

	err := Teardown(context.Background(), dc, fullConfig(), true, true)
	if err == nil {
		t.Fatal("teardown should report errors, not swallow them")
	}
	// Best-effort: all operations still attempted despite errors.
	if !log.has("delete-firewall:" + n.MasterFirewall()) {
		t.Error("firewall should still be attempted after DNS error")
	}
	if !log.has("delete-network:" + n.Network()) {
		t.Error("network should still be attempted after server error")
	}
}

func TestTeardown_ServerErrorDoesNotBlockFirewall(t *testing.T) {
	log := newOpLog()
	dc := setupTeardown(log)
	n := names()

	log.errors["delete-server:"+n.Server("master")] = fmt.Errorf("stuck")

	_ = Teardown(context.Background(), dc, fullConfig(), false, false)

	if !log.has("delete-firewall:" + n.MasterFirewall()) {
		t.Error("firewall not deleted after server error")
	}
	if !log.has("delete-network:" + n.Network()) {
		t.Error("network not deleted after server error")
	}
}

func TestTeardown_DNSErrorDoesNotBlockServers(t *testing.T) {
	log := newOpLog()
	dc := setupTeardown(log)
	n := names()

	log.errors["delete-dns:myapp.com"] = fmt.Errorf("dns fail")
	log.errors["delete-dns:www.myapp.com"] = fmt.Errorf("dns fail")

	_ = Teardown(context.Background(), dc, fullConfig(), false, false)

	if !log.has("delete-server:" + n.Server("master")) {
		t.Error("server not deleted after DNS errors")
	}
}

func TestTeardown_VolumeErrorDoesNotBlockServers(t *testing.T) {
	log := newOpLog()
	dc := setupTeardown(log)
	n := names()

	log.errors["delete-volume:"+n.Volume("pgdata")] = fmt.Errorf("volume busy")

	_ = Teardown(context.Background(), dc, fullConfig(), true, false)

	if !log.has("delete-server:" + n.Server("master")) {
		t.Error("server not deleted after volume error")
	}
}

// ── Tests: no k8s resources ───────────────────────────────────────────────────

func TestTeardown_NoK8sResourceOps(t *testing.T) {
	log := newOpLog()
	dc := setupTeardown(log)
	_ = Teardown(context.Background(), dc, fullConfig(), true, true)

	for _, op := range log.all() {
		if strings.Contains(op, "service") || strings.Contains(op, "cron") ||
			strings.Contains(op, "ingress") || strings.Contains(op, "secret") {
			t.Errorf("teardown should not touch k8s resources, but got: %s", op)
		}
	}
}

// ── Tests: edge cases ─────────────────────────────────────────────────────────

func TestTeardown_InvalidClusterNames(t *testing.T) {
	log := newOpLog()
	dc := setupTeardown(log)
	dc.Cluster.AppName = ""
	dc.Cluster.Env = ""

	cfg := &config.AppConfig{App: "", Env: "", Servers: map[string]config.ServerDef{}}

	err := Teardown(context.Background(), dc, cfg, false, false)
	if err == nil {
		t.Fatal("should return error with invalid names")
	}
}

func TestTeardown_DNSBeforeServers(t *testing.T) {
	log := newOpLog()
	dc := setupTeardown(log)
	n := names()
	_ = Teardown(context.Background(), dc, fullConfig(), false, false)

	dnsIdx := log.indexOf("delete-dns:myapp.com")
	srvIdx := log.indexOf("delete-server:" + n.Server("worker"))

	if dnsIdx < 0 || srvIdx < 0 {
		t.Fatalf("missing ops: %v", log.all())
	}
	if dnsIdx >= srvIdx {
		t.Errorf("DNS (%d) not before servers (%d)", dnsIdx, srvIdx)
	}
}

func TestTeardown_FullOrderDNS_Storage_Volumes_Workers_Masters_Firewall_Network(t *testing.T) {
	log := newOpLog()
	dc := setupTeardown(log)
	n := names()
	_ = Teardown(context.Background(), dc, fullConfig(), true, true)

	dns := log.indexOf("delete-dns:myapp.com")
	bucket := log.indexOf("delete-bucket:" + n.Bucket("assets"))
	vol := log.indexOf("delete-volume:" + n.Volume("pgdata"))
	worker := log.indexOf("delete-server:" + n.Server("worker"))
	master := log.indexOf("delete-server:" + n.Server("master"))
	fw := log.indexOf("delete-firewall:" + n.MasterFirewall())
	net := log.indexOf("delete-network:" + n.Network())

	for label, idx := range map[string]int{
		"dns": dns, "bucket": bucket, "volume": vol,
		"worker": worker, "master": master, "firewall": fw, "network": net,
	} {
		if idx < 0 {
			t.Fatalf("%s not found in ops: %v", label, log.all())
		}
	}

	// DNS before storage before volumes before workers before master before firewall before network
	if dns >= bucket {
		t.Errorf("dns (%d) not before bucket (%d)", dns, bucket)
	}
	if bucket >= vol {
		t.Errorf("bucket (%d) not before volume (%d)", bucket, vol)
	}
	if vol >= worker {
		t.Errorf("volume (%d) not before worker (%d)", vol, worker)
	}
	if worker >= master {
		t.Errorf("worker (%d) not before master (%d)", worker, master)
	}
	if master >= fw {
		t.Errorf("master (%d) not before firewall (%d)", master, fw)
	}
	if fw >= net {
		t.Errorf("firewall (%d) not before network (%d)", fw, net)
	}
}
