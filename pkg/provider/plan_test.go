package provider

import (
	"context"
	"testing"

	"github.com/getnvoi/nvoi/pkg/utils"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// planView is a minimal ProviderConfigView extended for ComputeInfraPlan
// tests. Permitted under the mock governance rules — ProviderConfigView is
// a data-view interface, not a provider interface.
type planView struct {
	servers []ServerSpec
	volumes []VolumeSpec
	rules   []string
	domains map[string][]string
	tunnel  string
}

func (v *planView) AppName() string                       { return "myapp" }
func (v *planView) EnvName() string                       { return "prod" }
func (v *planView) ServerDefs() []ServerSpec              { return v.servers }
func (v *planView) FirewallRules() []string               { return v.rules }
func (v *planView) VolumeDefs() []VolumeSpec              { return v.volumes }
func (v *planView) ServiceDefs() []ServiceSpec            { return nil }
func (v *planView) DomainsByService() map[string][]string { return v.domains }
func (v *planView) TunnelProvider() string                { return v.tunnel }

func testNames(t *testing.T) *utils.Names {
	t.Helper()
	n, err := utils.NewNames("myapp", "prod")
	if err != nil {
		t.Fatalf("NewNames: %v", err)
	}
	return n
}

func TestComputeInfraPlan_FirstDeploy(t *testing.T) {
	cfg := &planView{
		servers: []ServerSpec{
			{Name: "master", Type: "cax11", Region: "nbg1", Role: utils.RoleMaster},
			{Name: "worker-1", Type: "cax11", Region: "nbg1", Role: utils.RoleWorker},
		},
		volumes: []VolumeSpec{
			{Name: "pgdata", Size: 20, Server: "master"},
		},
	}
	got, err := ComputeInfraPlan(context.Background(), cfg, nil, testNames(t))
	if err != nil {
		t.Fatalf("ComputeInfraPlan: %v", err)
	}
	want := []PlanEntry{
		{Status: PlanAdd, Kind: KindServer, Name: "master", Detail: "cax11 nbg1"},
		{Status: PlanAdd, Kind: KindServer, Name: "worker-1", Detail: "cax11 nbg1"},
		{Status: PlanAdd, Kind: KindVolume, Name: "pgdata", Detail: "20GB on master"},
		{Status: PlanAdd, Kind: KindFirewall, Name: "nvoi-myapp-prod-master-fw"},
		{Status: PlanAdd, Kind: KindFirewall, Name: "nvoi-myapp-prod-worker-fw"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("first-deploy plan (-want +got):\n%s", diff)
	}
}

func TestComputeInfraPlan_Converged(t *testing.T) {
	cfg := &planView{
		servers: []ServerSpec{
			{Name: "master", Type: "cax11", Region: "nbg1", Role: utils.RoleMaster},
		},
	}
	snap := &LiveSnapshot{
		Servers:       []string{"master"},
		Firewalls:     []string{"nvoi-myapp-prod-master-fw"},
		FirewallRules: map[string]PortAllowList{"nvoi-myapp-prod-master-fw": nil},
	}
	got, err := ComputeInfraPlan(context.Background(), cfg, snap, testNames(t))
	if err != nil {
		t.Fatalf("ComputeInfraPlan: %v", err)
	}
	// Converged → every entry must be PlanNoChange (the inventory
	// baseline). Zero changes.
	for _, e := range got {
		if e.Status != PlanNoChange {
			t.Errorf("converged: expected PlanNoChange entries only, got %+v", e)
		}
	}
}

func TestComputeInfraPlan_AddAndDeleteServer(t *testing.T) {
	cfg := &planView{
		servers: []ServerSpec{
			{Name: "master", Type: "cax11", Region: "nbg1", Role: utils.RoleMaster},
			{Name: "worker-2", Type: "cax21", Region: "nbg1", Role: utils.RoleWorker},
		},
	}
	snap := &LiveSnapshot{
		Servers:       []string{"master", "worker-1"},
		Firewalls:     []string{"nvoi-myapp-prod-master-fw", "nvoi-myapp-prod-worker-fw"},
		FirewallRules: map[string]PortAllowList{},
	}
	got, err := ComputeInfraPlan(context.Background(), cfg, snap, testNames(t))
	if err != nil {
		t.Fatalf("ComputeInfraPlan: %v", err)
	}
	// Filter to changes (drop the unchanged-master + unchanged-firewall
	// baseline entries that the inventory now emits).
	got = filterChanges(got)
	want := []PlanEntry{
		{Status: PlanAdd, Kind: KindServer, Name: "worker-2", Detail: "cax21 nbg1"},
		{Status: PlanDelete, Kind: KindServer, Name: "worker-1"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("server diff (-want +got):\n%s", diff)
	}
}

func TestComputeInfraPlan_BuilderCacheVolumeSynthesized(t *testing.T) {
	cfg := &planView{
		servers: []ServerSpec{
			{Name: "master", Type: "cax11", Region: "nbg1", Role: utils.RoleMaster},
			{Name: "builder-1", Type: "cpx31", Region: "nbg1", Role: utils.RoleBuilder},
		},
	}
	got, err := ComputeInfraPlan(context.Background(), cfg, nil, testNames(t))
	if err != nil {
		t.Fatalf("ComputeInfraPlan: %v", err)
	}
	// Builder cache volume must show up as a synthesized add even though
	// it isn't in cfg.VolumeDefs(). Order-independent check: just verify
	// the cache name appears as an ADD entry.
	cacheName := testNames(t).BuilderCacheVolumeShort("builder-1")
	found := false
	for _, e := range got {
		if e.Status == PlanAdd && e.Kind == KindVolume && e.Name == cacheName {
			found = true
		}
	}
	if !found {
		t.Errorf("builder cache volume %q not in plan; got %#v", cacheName, got)
	}
}

func TestComputeInfraPlan_FirewallRule_Add(t *testing.T) {
	cfg := &planView{
		servers: []ServerSpec{
			{Name: "master", Type: "cax11", Region: "nbg1", Role: utils.RoleMaster},
		},
		rules: []string{"8080:0.0.0.0/0"},
	}
	snap := &LiveSnapshot{
		Servers:       []string{"master"},
		Firewalls:     []string{"nvoi-myapp-prod-master-fw"},
		FirewallRules: map[string]PortAllowList{"nvoi-myapp-prod-master-fw": nil},
	}
	got, err := ComputeInfraPlan(context.Background(), cfg, snap, testNames(t))
	if err != nil {
		t.Fatalf("ComputeInfraPlan: %v", err)
	}
	got = filterChanges(got)
	want := []PlanEntry{
		{Status: PlanAdd, Kind: KindFirewallRule, Name: "nvoi-myapp-prod-master-fw:8080", Detail: "[0.0.0.0/0]"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("rule add (-want +got):\n%s", diff)
	}
}

func TestComputeInfraPlan_FirewallRule_Delete_Destructive(t *testing.T) {
	// The footgun case: cfg has narrowed firewall rules; live has a port
	// open. Plan must surface the port removal so the operator gets a
	// chance to confirm before getting locked out.
	cfg := &planView{
		servers: []ServerSpec{
			{Name: "master", Type: "cax11", Region: "nbg1", Role: utils.RoleMaster},
		},
		// no domains, no tunnel, no rules → desired master fw is base-only
	}
	snap := &LiveSnapshot{
		Servers:   []string{"master"},
		Firewalls: []string{"nvoi-myapp-prod-master-fw"},
		FirewallRules: map[string]PortAllowList{
			"nvoi-myapp-prod-master-fw": {"80": {"0.0.0.0/0", "::/0"}},
		},
	}
	got, err := ComputeInfraPlan(context.Background(), cfg, snap, testNames(t))
	if err != nil {
		t.Fatalf("ComputeInfraPlan: %v", err)
	}
	changes := filterChanges(got)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change entry, got %#v", changes)
	}
	e := changes[0]
	if e.Status != PlanDelete || e.Kind != KindFirewallRule || e.Name != "nvoi-myapp-prod-master-fw:80" {
		t.Errorf("expected port-80 delete, got %+v", e)
	}
	if !e.Promptable() {
		t.Errorf("destructive rule delete must be Promptable")
	}
}

func TestComputeInfraPlan_FirewallRule_Update_CIDRChange(t *testing.T) {
	cfg := &planView{
		servers: []ServerSpec{
			{Name: "master", Type: "cax11", Region: "nbg1", Role: utils.RoleMaster},
		},
		rules: []string{"8080:10.0.0.0/8"},
	}
	snap := &LiveSnapshot{
		Servers:   []string{"master"},
		Firewalls: []string{"nvoi-myapp-prod-master-fw"},
		FirewallRules: map[string]PortAllowList{
			"nvoi-myapp-prod-master-fw": {"8080": {"0.0.0.0/0"}},
		},
	}
	got, err := ComputeInfraPlan(context.Background(), cfg, snap, testNames(t))
	if err != nil {
		t.Fatalf("ComputeInfraPlan: %v", err)
	}
	changes := filterChanges(got)
	if len(changes) != 1 || changes[0].Status != PlanUpdate || changes[0].Kind != KindFirewallRule {
		t.Fatalf("expected one rule update entry, got %#v", changes)
	}
}

// Regression: port 22 was emitted as a destructive DELETE on every
// deploy because GetFirewallRules returns the default-open SSH rule
// in `live` while FirewallAllowList omits it from `desired` (it's
// added separately in buildFirewallRules). Treating that as DELETE
// would prompt the operator with a "removes SSH access" warning on
// every redeploy.
func TestComputeInfraPlan_FirewallRule_SSHNotInDesired_NoDelete(t *testing.T) {
	cfg := &planView{
		servers: []ServerSpec{
			{Name: "master", Type: "cax11", Region: "nbg1", Role: utils.RoleMaster},
		},
		// No user-overridden firewall rules → desired master rules
		// nil → "22" not in desired.
	}
	snap := &LiveSnapshot{
		Servers:   []string{"master"},
		Firewalls: []string{"nvoi-myapp-prod-master-fw"},
		FirewallRules: map[string]PortAllowList{
			// Live includes "22" (from buildFirewallRules' default-open
			// SSH rule). Plan must NOT emit a DELETE entry.
			"nvoi-myapp-prod-master-fw": {"22": {"0.0.0.0/0", "::/0"}},
		},
	}
	got, err := ComputeInfraPlan(context.Background(), cfg, snap, testNames(t))
	if err != nil {
		t.Fatalf("ComputeInfraPlan: %v", err)
	}
	for _, e := range got {
		if e.Kind == KindFirewallRule && e.Status == PlanDelete {
			t.Errorf("port-22 false DELETE: %+v", e)
		}
	}
}

// Regression: when the user explicitly overrides SSH (e.g.
// `firewall: 22:1.2.3.4/32`), CIDR drift on port 22 SHOULD still
// surface as an UPDATE — the SSH-skip applies only to "live has 22 but
// desired doesn't", not to legitimate user overrides.
func TestComputeInfraPlan_FirewallRule_SSHUserOverride_DiffsNormally(t *testing.T) {
	cfg := &planView{
		servers: []ServerSpec{
			{Name: "master", Type: "cax11", Region: "nbg1", Role: utils.RoleMaster},
		},
		rules: []string{"22:1.2.3.4/32"},
	}
	snap := &LiveSnapshot{
		Servers:   []string{"master"},
		Firewalls: []string{"nvoi-myapp-prod-master-fw"},
		FirewallRules: map[string]PortAllowList{
			"nvoi-myapp-prod-master-fw": {"22": {"0.0.0.0/0", "::/0"}}, // wide open
		},
	}
	got, err := ComputeInfraPlan(context.Background(), cfg, snap, testNames(t))
	if err != nil {
		t.Fatalf("ComputeInfraPlan: %v", err)
	}
	found := false
	for _, e := range got {
		if e.Kind == KindFirewallRule && e.Status == PlanUpdate && e.Name == "nvoi-myapp-prod-master-fw:22" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected UPDATE for user-overridden port 22, got %#v", got)
	}
}

func TestComputeInfraPlan_FirewallRule_NoChangeOnReorder(t *testing.T) {
	cfg := &planView{
		servers: []ServerSpec{
			{Name: "master", Type: "cax11", Region: "nbg1", Role: utils.RoleMaster},
		},
		rules: []string{"8080:1.1.1.1/32,2.2.2.2/32"},
	}
	snap := &LiveSnapshot{
		Servers:   []string{"master"},
		Firewalls: []string{"nvoi-myapp-prod-master-fw"},
		FirewallRules: map[string]PortAllowList{
			// Same CIDRs in reversed order — must not register as a change.
			"nvoi-myapp-prod-master-fw": {"8080": {"2.2.2.2/32", "1.1.1.1/32"}},
		},
	}
	got, err := ComputeInfraPlan(context.Background(), cfg, snap, testNames(t))
	if err != nil {
		t.Fatalf("ComputeInfraPlan: %v", err)
	}
	if changes := filterChanges(got); len(changes) != 0 {
		t.Errorf("CIDR reorder should not produce diff; got %#v", changes)
	}
}

func TestComputeInfraPlan_NewFirewallSuppressesRuleEntries(t *testing.T) {
	// When a firewall is being CREATED, per-port rule entries are
	// implicit in the ResFirewall add — don't double-emit them.
	cfg := &planView{
		servers: []ServerSpec{
			{Name: "master", Type: "cax11", Region: "nbg1", Role: utils.RoleMaster},
			{Name: "worker-1", Type: "cax11", Region: "nbg1", Role: utils.RoleWorker},
		},
		rules: []string{"8080:0.0.0.0/0"}, // would imply rule on master fw, but master fw is being created
	}
	snap := &LiveSnapshot{
		// Master exists but worker is new → worker firewall doesn't exist yet.
		Servers:       []string{"master", "worker-1"},
		Firewalls:     []string{"nvoi-myapp-prod-master-fw"},
		FirewallRules: map[string]PortAllowList{"nvoi-myapp-prod-master-fw": {"8080": {"0.0.0.0/0"}}},
	}
	got, err := ComputeInfraPlan(context.Background(), cfg, snap, testNames(t))
	if err != nil {
		t.Fatalf("ComputeInfraPlan: %v", err)
	}
	// Expect: ResFirewall add for worker-fw. NO rule entries for it.
	for _, e := range got {
		if e.Kind == KindFirewallRule && e.Name == "nvoi-myapp-prod-worker-fw:0" {
			t.Errorf("rule entry emitted for new firewall; got %+v", e)
		}
	}
	// Confirm the worker firewall add IS present.
	found := false
	for _, e := range got {
		if e.Status == PlanAdd && e.Kind == KindFirewall && e.Name == "nvoi-myapp-prod-worker-fw" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected worker firewall add; got %#v", got)
	}
}

func TestPlanEntry_Promptable(t *testing.T) {
	cases := []struct {
		name string
		e    PlanEntry
		want bool
	}{
		{"add-no-reason-prompts", PlanEntry{Status: PlanAdd, Reason: ""}, true},
		{"image-tag-update-skips", PlanEntry{Status: PlanUpdate, Reason: "image-tag"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.e.Promptable(); got != tc.want {
				t.Errorf("Promptable() = %v, want %v", got, tc.want)
			}
		})
	}
}

// Compile-time sanity: stubViews satisfy ProviderConfigView.
var (
	_ ProviderConfigView = (*planView)(nil)
)

// Silence unused-import when no slice-comparison helpers are referenced.
var _ = cmpopts.EquateEmpty

// filterChanges drops PlanNoChange entries — used by tests that
// assert on the change-set, not the full inventory. Mirror of the
// reconcile.Plan.Changes() helper without the wrapper struct.
func filterChanges(in []PlanEntry) []PlanEntry {
	out := make([]PlanEntry, 0, len(in))
	for _, e := range in {
		if e.Status != PlanNoChange {
			out = append(out, e)
		}
	}
	return out
}
