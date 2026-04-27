package reconcile

import (
	"context"
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/internal/testutil"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// ── Plan struct helpers ───────────────────────────────────────────────────────

func TestPlan_IsEmpty(t *testing.T) {
	if !(&Plan{}).IsEmpty() {
		t.Errorf("zero-entry plan must be empty")
	}
	p := &Plan{Entries: []provider.PlanEntry{{Kind: provider.PlanAdd, Resource: provider.ResServer}}}
	if p.IsEmpty() {
		t.Errorf("plan with entries must not be empty")
	}
}

func TestPlan_HasInfraChanges(t *testing.T) {
	cases := []struct {
		name    string
		entries []provider.PlanEntry
		want    bool
	}{
		{"empty", nil, false},
		{"server-add", []provider.PlanEntry{{Resource: provider.ResServer}}, true},
		{"firewall-rule-delete", []provider.PlanEntry{{Resource: provider.ResFirewallRule}}, true},
		{"dns-add", []provider.PlanEntry{{Resource: provider.ResDNS}}, true},
		{"workload-only", []provider.PlanEntry{{Resource: provider.ResWorkload}}, false},
		{"registry-only", []provider.PlanEntry{{Resource: provider.ResRegistrySecret}}, false},
		{"mixed", []provider.PlanEntry{{Resource: provider.ResWorkload}, {Resource: provider.ResServer}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &Plan{Entries: tc.entries}
			if got := p.HasInfraChanges(); got != tc.want {
				t.Errorf("HasInfraChanges() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPlan_Promptable_FiltersReasonFlagged(t *testing.T) {
	p := &Plan{Entries: []provider.PlanEntry{
		{Kind: provider.PlanAdd, Resource: provider.ResServer},
		{Kind: provider.PlanUpdate, Resource: provider.ResWorkload, Reason: "image-tag"},
		{Kind: provider.PlanDelete, Resource: provider.ResFirewallRule},
	}}
	got := p.Promptable()
	if len(got) != 2 {
		t.Fatalf("expected 2 promptable entries, got %d: %#v", len(got), got)
	}
	for _, e := range got {
		if e.Reason != "" {
			t.Errorf("Promptable() returned a Reason-flagged entry: %+v", e)
		}
	}
}

// ── planRegistries ────────────────────────────────────────────────────────────

func TestPlanRegistries_AddWhenSecretMissing(t *testing.T) {
	dc := testDC(convergeMock())
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Registry: map[string]config.RegistryDef{
			"ghcr.io": {Username: "user", Password: "pass"},
		},
	}
	got, err := planRegistries(context.Background(), dc, cfg)
	if err != nil {
		t.Fatalf("planRegistries: %v", err)
	}
	if len(got) != 1 || got[0].Kind != provider.PlanAdd ||
		got[0].Resource != provider.ResRegistrySecret ||
		got[0].Name != kube.PullSecretName {
		t.Errorf("expected single ADD entry for %s, got %#v", kube.PullSecretName, got)
	}
}

func TestPlanRegistries_DeleteWhenOrphan(t *testing.T) {
	dc := testDC(convergeMock())
	// Pre-seed an orphaned pull secret as if a previous deploy created
	// it; cfg now has no registry block.
	kf := kfFor(dc)
	names, _ := dc.Cluster.Names()
	ns := names.KubeNamespace()
	if err := kf.Client.EnsureSecret(context.Background(), ns, utils.OwnerRegistries, kube.PullSecretName, map[string]string{".dockerconfigjson": "{}"}); err != nil {
		t.Fatalf("seed orphan secret: %v", err)
	}

	cfg := &config.AppConfig{App: "myapp", Env: "prod"} // no Registry block
	got, err := planRegistries(context.Background(), dc, cfg)
	if err != nil {
		t.Fatalf("planRegistries: %v", err)
	}
	if len(got) != 1 || got[0].Kind != provider.PlanDelete ||
		got[0].Resource != provider.ResRegistrySecret ||
		got[0].Name != kube.PullSecretName {
		t.Errorf("expected single DELETE entry for orphan %s, got %#v", kube.PullSecretName, got)
	}
}

func TestPlanRegistries_NoChange_BothEmpty(t *testing.T) {
	dc := testDC(convergeMock())
	cfg := &config.AppConfig{App: "myapp", Env: "prod"} // no Registry, no secret seeded
	got, err := planRegistries(context.Background(), dc, cfg)
	if err != nil {
		t.Fatalf("planRegistries: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty plan when nothing changes, got %#v", got)
	}
}

// ── planRouteDomains ──────────────────────────────────────────────────────────

func TestPlanRouteDomains_AddWhenDomainMissing(t *testing.T) {
	cf := testutil.NewCloudflareFake(t, testutil.CloudflareFakeOptions{ZoneID: "zone1", ZoneDomain: "myapp.com"})
	cf.RegisterDNS("test-dns-plan-add")

	dc := testDC(convergeMock())
	dc.DNS = app.ProviderRef{
		Name:  "test-dns-plan-add",
		Creds: map[string]string{"api_token": "x", "zone": "myapp.com", "zone_id": "zone1"},
	}

	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Domains: map[string][]string{"web": {"api.myapp.com"}},
	}
	got, err := planRouteDomains(context.Background(), dc, cfg)
	if err != nil {
		t.Fatalf("planRouteDomains: %v", err)
	}
	if len(got) != 1 || got[0].Kind != provider.PlanAdd ||
		got[0].Resource != provider.ResDNS ||
		got[0].Name != "api.myapp.com" {
		t.Errorf("expected single DNS ADD for api.myapp.com, got %#v", got)
	}
}

func TestPlanRouteDomains_NoChange_DomainAlreadyLive(t *testing.T) {
	cf := testutil.NewCloudflareFake(t, testutil.CloudflareFakeOptions{ZoneID: "zone1", ZoneDomain: "myapp.com"})
	cf.RegisterDNS("test-dns-plan-nochange")
	// Seed an existing record matching the cfg → planner sees no
	// add/delete.
	cf.SeedDNSRecord("api.myapp.com", "1.2.3.4", "A")

	dc := testDC(convergeMock())
	dc.DNS = app.ProviderRef{
		Name:  "test-dns-plan-nochange",
		Creds: map[string]string{"api_token": "x", "zone": "myapp.com", "zone_id": "zone1"},
	}

	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Domains: map[string][]string{"web": {"api.myapp.com"}},
	}
	got, err := planRouteDomains(context.Background(), dc, cfg)
	if err != nil {
		t.Fatalf("planRouteDomains: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty plan when domain already routed, got %#v", got)
	}
}

// ── ComputePlan integration ───────────────────────────────────────────────────

// TestComputePlan_FreshCluster_EmitsInfraAdds verifies the orchestrator
// stitches together the infra planner + downstream step planners. With a
// fake that has only a master server (no firewall rules), cfg declaring
// the same master + a public domain + tunnel-less Caddy mode, we expect:
//
//   - infra: nothing from servers (master matches), nothing from firewalls
//     (master-fw seeded), but rules ADDed (cfg has 80/443 derived from the
//     domain in Caddy mode, fake's seeded master-fw has no rules).
func TestComputePlan_DetectsFirewallRuleDriftFromCfg(t *testing.T) {
	var log opLog
	dc := convergeDC(&log, convergeMock())

	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Providers: config.ProvidersDef{Infra: "test-reconcile"},
		Servers:   map[string]config.ServerDef{"master": {Type: "cax11", Region: "nbg1", Role: "master"}},
		Services:  map[string]config.ServiceDef{"web": {Image: "nginx", Port: 80}},
		Domains:   map[string][]string{"web": {"app.myapp.com"}},
	}

	plan, err := ComputePlan(context.Background(), dc, cfg)
	if err != nil {
		t.Fatalf("ComputePlan: %v", err)
	}
	// Cfg adds 80 + 443 to the master firewall (Caddy mode auto-derives).
	// The seeded fake firewall has no rules → expect rule ADDs.
	addedPorts := map[string]bool{}
	for _, e := range plan.Entries {
		if e.Resource == provider.ResFirewallRule && e.Kind == provider.PlanAdd {
			// Name is "<fwname>:<port>" — split off the port.
			name := e.Name
			for i := len(name) - 1; i >= 0; i-- {
				if name[i] == ':' {
					addedPorts[name[i+1:]] = true
					break
				}
			}
		}
	}
	if !addedPorts["80"] || !addedPorts["443"] {
		t.Errorf("expected ADD entries for ports 80 and 443, got %v (full plan: %#v)", addedPorts, plan.Entries)
	}
	if !plan.HasInfraChanges() {
		t.Errorf("plan with firewall-rule entries must report HasInfraChanges()")
	}
}

func TestComputePlan_Converged_NoEntries(t *testing.T) {
	// Use the activeHetzner from convergeDC and pre-seed master rules to
	// match what cfg expects, so the planner sees zero diff.
	var log opLog
	dc := convergeDC(&log, convergeMock())

	// Pre-stamp the master firewall with the rules cfg will compute, so
	// the rule diff is empty. cfg has no domains → master rules are nil
	// (base only). Already the seeded state.
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Providers: config.ProvidersDef{Infra: "test-reconcile"},
		Servers:   map[string]config.ServerDef{"master": {Type: "cax11", Region: "nbg1", Role: "master"}},
		Services:  map[string]config.ServiceDef{"web": {Image: "nginx", Port: 80}},
		// no domains, no tunnel → no Caddy 80/443 → master rules nil
	}

	plan, err := ComputePlan(context.Background(), dc, cfg)
	if err != nil {
		t.Fatalf("ComputePlan: %v", err)
	}
	// Allow a worker-fw deletion entry IFF the seeded fake had one but
	// cfg has no workers. convergeDC seeds worker-fw too, and cfg has no
	// workers — that's a legitimate firewall existence DELETE.
	for _, e := range plan.Entries {
		if e.Resource == provider.ResFirewall && e.Kind == provider.PlanDelete && e.Name == "nvoi-myapp-prod-worker-fw" {
			continue // expected
		}
		t.Errorf("unexpected entry in converged plan: %+v", e)
	}
}
