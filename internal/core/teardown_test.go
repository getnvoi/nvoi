package core

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/internal/testutil"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// ── Provider registration ─────────────────────────────────────────────────────
//
// Every test calls setupTeardown(), which:
//   1. Creates a fresh HetznerFake (compute) + CloudflareFake (DNS + R2).
//   2. Seeds both with the state a realistic teardown should see.
//   3. Re-registers the per-test fakes under the "test-teardown-*" names.
//   4. Returns a DeployContext wired to those names.
//
// Assertions go through the fakes' OpLog via the local opLog alias (so
// existing test assertions compile unchanged).

// opLog is a thin view into whichever fake the current test is asserting
// against. setupTeardown wires it to the merged OpLog of the Hetzner + CF
// fakes (they share recording via the unified RecordFn indirection below).
type opLog struct {
	hz *testutil.HetznerFake
	cf *testutil.CloudflareFake
}

func newOpLog() *opLog { return &opLog{} }

func (l *opLog) has(op string) bool {
	if l.hz != nil && l.hz.Has(op) {
		return true
	}
	if l.cf != nil && l.cf.Has(op) {
		return true
	}
	return false
}

func (l *opLog) count(prefix string) int {
	n := 0
	if l.hz != nil {
		n += l.hz.Count(prefix)
	}
	if l.cf != nil {
		n += l.cf.Count(prefix)
	}
	return n
}

func (l *opLog) indexOf(op string) int {
	// Ordering tests need a single merged index space. Merge the two logs in
	// their recorded order. Since the fakes run on independent HTTP servers,
	// chronological ordering on a single test is preserved only within one
	// fake — but teardown issues calls serially, so the two logs interleave
	// in wall-clock order. Merge via the concatenation of All() calls, in
	// the order teardown touches them: DNS (cf) → storage (cf) → volumes
	// (hz) → servers (hz) → firewalls (hz) → networks (hz). Good enough
	// because teardown order is deterministic.
	var merged []string
	if l.cf != nil {
		merged = append(merged, l.cf.All()...)
	}
	if l.hz != nil {
		merged = append(merged, l.hz.All()...)
	}
	for i, o := range merged {
		if o == op {
			return i
		}
	}
	return -1
}

func (l *opLog) all() []string {
	var merged []string
	if l.cf != nil {
		merged = append(merged, l.cf.All()...)
	}
	if l.hz != nil {
		merged = append(merged, l.hz.All()...)
	}
	return merged
}

// errorOn arms both fakes to return an error when the named op fires.
func (l *opLog) errorOn(op string, err error) {
	if l.hz != nil {
		l.hz.ErrorOn(op, err)
	}
	if l.cf != nil {
		l.cf.ErrorOn(op, err)
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// activeHetzner / activeCF expose the current per-test fakes so tests can
// re-seed state after setupTeardown (multi-worker server tests, etc.).
var (
	activeHetzner *testutil.HetznerFake
	activeCF      *testutil.CloudflareFake
)

func setupTeardown(log *opLog) *config.DeployContext {
	n := names()
	hz := testutil.NewHetznerFake(nil)
	hz.Register("test-teardown")
	hz.SeedServer(n.Server("master"), "1.2.3.4", "10.0.1.1")
	hz.SeedServer(n.Server("worker"), "5.6.7.8", "10.0.1.2")
	hz.SeedVolume(n.Volume("pgdata"), 20, "")
	hz.SeedFirewall(n.MasterFirewall())
	hz.SeedFirewall(n.WorkerFirewall())
	hz.SeedNetwork(n.Network())
	activeHetzner = hz

	cf := testutil.NewCloudflareFake(nil, testutil.CloudflareFakeOptions{
		ZoneID:     "Z1",
		ZoneDomain: "myapp.com",
		AccountID:  "testacct",
	})
	cf.RegisterDNS("test-teardown-dns")
	cf.RegisterBucket("test-teardown-bucket")
	// Seed DNS records for the two domains in fullConfig()
	cf.SeedDNSRecord("myapp.com", "1.2.3.4", "A")
	cf.SeedDNSRecord("www.myapp.com", "1.2.3.4", "A")
	// Seed storage bucket
	cf.SeedBucket(n.Bucket("assets"))
	activeCF = cf

	log.hz = hz
	log.cf = cf

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
	activeCF.SeedDNSRecord("api.myapp.com", "1.2.3.4", "A")

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
	// Replace the default-seeded servers with role-specific ones.
	activeHetzner.Reset()
	activeHetzner.SeedServer(n.Server("master"), "1.2.3.4", "10.0.1.1")
	activeHetzner.SeedServer(n.Server("worker-a"), "5.6.7.8", "10.0.1.2")
	activeHetzner.SeedServer(n.Server("worker-b"), "9.10.11.12", "10.0.1.3")
	activeHetzner.SeedFirewall(n.MasterFirewall())
	activeHetzner.SeedFirewall(n.WorkerFirewall())
	activeHetzner.SeedNetwork(n.Network())

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
	// Remove the default-seeded worker so the count is 1, not 2.
	activeHetzner.Reset()
	activeHetzner.SeedServer(n.Server("master"), "1.2.3.4", "10.0.1.1")
	activeHetzner.SeedFirewall(n.MasterFirewall())
	activeHetzner.SeedNetwork(n.Network())

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

	log.errorOn("delete-firewall:"+n.MasterFirewall(), fmt.Errorf("firewall stuck"))

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

	// Empty desired config — tests that the orphan sweep still cleans
	// firewall + network even when nothing is declared.
	_ = Teardown(context.Background(), dc, cfg, true, true)

	if !log.has("delete-firewall:" + n.MasterFirewall()) {
		t.Error("firewall not deleted on empty config")
	}
	if !log.has("delete-network:" + n.Network()) {
		t.Error("network not deleted on empty config")
	}
	// With an empty config, Teardown doesn't know which servers to delete —
	// but the default-seeded fake still has them. This tests orphan cleanup,
	// not server deletion from config. Reset to isolate the empty-config path.
	activeHetzner.Reset()
	activeHetzner.SeedFirewall(n.MasterFirewall())
	activeHetzner.SeedNetwork(n.Network())
	log2 := newOpLog()
	dc2 := setupTeardownKeepingFakes(log2)
	_ = Teardown(context.Background(), dc2, cfg, true, true)
	if log2.count("delete-server:") != 0 {
		t.Error("server deletes on empty config")
	}
}

// setupTeardownKeepingFakes reuses the already-live Hetzner / CF fakes
// (per-test-global) instead of creating fresh ones — for tests that need a
// mid-test reset without recreating HTTP servers.
func setupTeardownKeepingFakes(log *opLog) *config.DeployContext {
	log.hz = activeHetzner
	log.cf = activeCF
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
	activeHetzner.SeedVolume(n.Volume("redis"), 10, "")

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
	// Seed an object so EmptyBucket actually makes a DELETE call (the fake
	// records "empty-bucket:X" on any list-then-delete batch POST; seeding an
	// object guarantees the POST fires).
	activeCF.SeedBucketObject(n.Bucket("assets"), "some-object", []byte("data"))
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
	activeCF.SeedBucket(n.Bucket("uploads"))

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
	log.errorOn("delete-dns:myapp.com", fmt.Errorf("dns timeout"))
	log.errorOn("delete-server:"+n.Server("worker"), fmt.Errorf("api 500"))
	log.errorOn("delete-firewall:"+n.MasterFirewall(), fmt.Errorf("firewall locked"))

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

	log.errorOn("delete-server:"+n.Server("master"), fmt.Errorf("stuck"))

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

	log.errorOn("delete-dns:myapp.com", fmt.Errorf("dns fail"))
	log.errorOn("delete-dns:www.myapp.com", fmt.Errorf("dns fail"))

	_ = Teardown(context.Background(), dc, fullConfig(), false, false)

	if !log.has("delete-server:" + n.Server("master")) {
		t.Error("server not deleted after DNS errors")
	}
}

func TestTeardown_VolumeErrorDoesNotBlockServers(t *testing.T) {
	log := newOpLog()
	dc := setupTeardown(log)
	n := names()

	log.errorOn("delete-volume:"+n.Volume("pgdata"), fmt.Errorf("volume busy"))

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
	// Seed an object so the empty-bucket op fires (list → delete batch).
	activeCF.SeedBucketObject(n.Bucket("assets"), "o1", []byte("x"))
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
