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
	// One PlanNoChange entry expected for the already-routed domain;
	// no add/delete entries.
	for _, e := range got {
		if e.Kind != provider.PlanNoChange {
			t.Errorf("expected only PlanNoChange entries, got %+v", e)
		}
	}
}

// ── stripDeployHash + image-tag detection ─────────────────────────────────────

func TestStripDeployHash(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"docker.io/foo/bar:20260427-100000", "docker.io/foo/bar"},       // no user tag → colon-separated hash
		{"docker.io/foo/bar:v2-20260427-100000", "docker.io/foo/bar:v2"}, // user tag → dash-separated hash
		{"foo/bar:v1", "foo/bar:v1"},                                     // no hash, untouched
		{"foo/bar@sha256:abc", "foo/bar@sha256:abc"},                     // digest pin, untouched
		{"plain", "plain"}, // no separator pattern
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := stripDeployHash(tc.in); got != tc.want {
				t.Errorf("stripDeployHash(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestPlanServices_ImageTagOnly_FlagsAuto verifies the load-bearing
// 99% case: a service with `build:` set, already deployed, gets a new
// hash on every deploy → the only diff is the trailing -YYYYMMDD-HHMMSS,
// and that update must carry Reason="image-tag" so it auto-applies
// instead of prompting.
func TestPlanServices_ImageTagOnly_FlagsAuto(t *testing.T) {
	dc := testDC(convergeMock())
	dc.Cluster.DeployHash = "20260427-200000"
	kf := kfFor(dc)
	names, _ := dc.Cluster.Names()
	ns := names.KubeNamespace()

	// Seed a Deployment as if a previous deploy created it. Image
	// matches what ResolveImage would produce minus the hash → the
	// only diff this run will see is the per-deploy hash.
	kf.SeedDeployment(ns, "api", utils.OwnerServices,
		"docker.io/deemx/nvoi-api:20260427-100000")

	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Registry: map[string]config.RegistryDef{"docker.io": {Username: "u", Password: "p"}},
		Services: map[string]config.ServiceDef{
			"api": {
				Image: "deemx/nvoi-api",
				Build: &config.BuildSpec{Context: "./"},
			},
		},
	}

	got, err := planServices(context.Background(), dc, cfg)
	if err != nil {
		t.Fatalf("planServices: %v", err)
	}
	var hit *provider.PlanEntry
	for i := range got {
		if got[i].Resource == provider.ResWorkload && got[i].Name == "api" && got[i].Kind == provider.PlanUpdate {
			hit = &got[i]
		}
	}
	if hit == nil {
		t.Fatalf("expected an image-tag UPDATE entry for api, got %#v", got)
	}
	if hit.Reason != "image-tag" {
		t.Errorf("expected Reason=image-tag (auto-skip), got %q (full entry: %+v)", hit.Reason, hit)
	}
	if hit.Promptable() {
		t.Errorf("image-tag UPDATE must NOT be promptable")
	}
}

// Regression: `nvoi plan` (DeployHash unset, read-only) was emitting
// the image-tag UPDATE entry as if a rebuild had happened — but plan
// is read-only and never builds. Operator saw "image rebuilt" on a
// no-op `nvoi plan` and (rightly) called it weird. The entry must
// only surface on `nvoi deploy` where BuildImages actually ran.
func TestPlanServices_ImageTagOnly_SuppressedOnPlanOnly(t *testing.T) {
	dc := testDC(convergeMock())
	dc.Cluster.DeployHash = "" // mimic `nvoi plan` standalone
	kf := kfFor(dc)
	names, _ := dc.Cluster.Names()
	ns := names.KubeNamespace()
	kf.SeedDeployment(ns, "api", utils.OwnerServices,
		"docker.io/deemx/nvoi-api:20260427-100000")

	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Registry: map[string]config.RegistryDef{"docker.io": {Username: "u", Password: "p"}},
		Services: map[string]config.ServiceDef{
			"api": {Image: "deemx/nvoi-api", Build: &config.BuildSpec{Context: "./"}},
		},
	}
	got, err := planServices(context.Background(), dc, cfg)
	if err != nil {
		t.Fatalf("planServices: %v", err)
	}
	for _, e := range got {
		if e.Resource == provider.ResWorkload && e.Name == "api" && e.Reason == "image-tag" {
			t.Errorf("plan-only run emitted image-tag entry; should be suppressed: %+v", e)
		}
	}
}

// TestPlanServices_FullImageChange_PromptsUser verifies the inverse:
// when the user changes the registry host or repo, the entry must be
// a non-auto UPDATE (Reason="" → Promptable=true).
// Regression: the per-service Secret holds expansion keys (one ENDPOINT
// /BUCKET/AKID/SAK quad per `storage:` ref + one DATABASE_URL_<NAME>
// per `databases:` ref). desiredSecretKeys was returning only the
// `secrets:` entries, so every storage/database expansion in the live
// Secret got false-flagged as an orphan DELETE on every deploy.
func TestDesiredSecretKeys_IncludesStorageAndDatabaseExpansions(t *testing.T) {
	got := desiredSecretKeys(
		[]string{"JWT_SECRET", "ALIAS=$ENC_KEY"},
		[]string{"assets"},
		[]string{"main", "DB_URL=other"},
	)
	want := map[string]bool{
		"JWT_SECRET":                       true,
		"ALIAS":                            true,
		"STORAGE_ASSETS_ENDPOINT":          true,
		"STORAGE_ASSETS_BUCKET":            true,
		"STORAGE_ASSETS_ACCESS_KEY_ID":     true,
		"STORAGE_ASSETS_SECRET_ACCESS_KEY": true,
		"DATABASE_URL_MAIN":                true,
		"DB_URL":                           true,
	}
	gotSet := map[string]bool{}
	for _, k := range got {
		gotSet[k] = true
	}
	for k := range want {
		if !gotSet[k] {
			t.Errorf("missing expected key %q in desiredSecretKeys output: got %v", k, got)
		}
	}
	for k := range gotSet {
		if !want[k] {
			t.Errorf("unexpected key %q in desiredSecretKeys output", k)
		}
	}
}

func TestPlanServices_FullImageChange_PromptsUser(t *testing.T) {
	dc := testDC(convergeMock())
	dc.Cluster.DeployHash = "20260427-200000"
	kf := kfFor(dc)
	names, _ := dc.Cluster.Names()
	ns := names.KubeNamespace()

	kf.SeedDeployment(ns, "api", utils.OwnerServices,
		"docker.io/deemx/nvoi-api:20260427-100000")

	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Registry: map[string]config.RegistryDef{"ghcr.io": {Username: "u", Password: "p"}},
		Services: map[string]config.ServiceDef{
			"api": {
				Image: "ghcr.io/deemx/nvoi-api", // different host
				Build: &config.BuildSpec{Context: "./"},
			},
		},
	}
	got, err := planServices(context.Background(), dc, cfg)
	if err != nil {
		t.Fatalf("planServices: %v", err)
	}
	for _, e := range got {
		if e.Resource == provider.ResWorkload && e.Name == "api" && e.Kind == provider.PlanUpdate {
			if e.Reason != "" {
				t.Errorf("registry host change must prompt, got Reason=%q", e.Reason)
			}
			return
		}
	}
	t.Errorf("expected a full UPDATE entry for api, got %#v", got)
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
	// Use the activeHetzner from convergeDC and align cfg so the planner
	// sees zero diff. cfg declares only the master (matches the seeded
	// fake), no services (no workload ADDs from planServices), no
	// domains (master rules base-only matches the seeded empty rules).
	var log opLog
	dc := convergeDC(&log, convergeMock())

	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Providers: config.ProvidersDef{Infra: "test-reconcile"},
		Servers:   map[string]config.ServerDef{"master": {Type: "cax11", Region: "nbg1", Role: "master"}},
		// no services, no crons, no domains, no tunnel — nothing to diff
	}

	plan, err := ComputePlan(context.Background(), dc, cfg)
	if err != nil {
		t.Fatalf("ComputePlan: %v", err)
	}
	// The seeded fake has worker-fw but cfg has no workers — that's a
	// legitimate firewall existence DELETE in the change-set.
	// PlanNoChange entries are inventory baseline and ignored here —
	// what matters is plan.Changes() contains ONLY the worker-fw delete.
	changes := plan.Changes()
	if len(changes) != 1 {
		t.Fatalf("expected exactly 1 change (worker-fw delete), got %#v", changes)
	}
	c := changes[0]
	if c.Kind != provider.PlanDelete || c.Resource != provider.ResFirewall || c.Name != "nvoi-myapp-prod-worker-fw" {
		t.Errorf("expected worker-fw delete, got %+v", c)
	}
}
