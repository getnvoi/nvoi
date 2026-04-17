package reconcile

import (
	"context"
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
)

// cfgWith returns a valid-enough AppConfig for Firewall() — pre-resolved
// firewall names and the given server map.
func cfgWith(servers map[string]config.ServerDef) *config.AppConfig {
	return &config.AppConfig{
		App:            "myapp",
		Env:            "prod",
		Firewall:       []string{"default"},
		Servers:        servers,
		MasterFirewall: "nvoi-myapp-prod-master-fw",
		WorkerFirewall: "nvoi-myapp-prod-worker-fw",
	}
}

func masterOnly() map[string]config.ServerDef {
	return map[string]config.ServerDef{
		"master": {Type: "cax11", Region: "nbg1", Role: "master"},
	}
}

func masterAndWorker() map[string]config.ServerDef {
	return map[string]config.ServerDef{
		"master": {Type: "cax11", Region: "nbg1", Role: "master"},
		"worker": {Type: "cax11", Region: "nbg1", Role: "worker"},
	}
}

// ── Firewall: desired-set reconciliation ─────────────────────────────────────

func TestFirewall_NoWorkers_OnlyMasterReconciled(t *testing.T) {
	log := &opLog{}
	dc := convergeDC(log, convergeMock())
	n := testNames()

	if err := Firewall(context.Background(), dc, nil, cfgWith(masterOnly())); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !log.has("firewall:" + n.MasterFirewall()) {
		t.Errorf("master firewall must be reconciled: %v", log.all())
	}
	if log.has("firewall:" + n.WorkerFirewall()) {
		t.Errorf("worker firewall must NOT be created when config has no workers: %v", log.all())
	}
}

func TestFirewall_WorkersPresent_BothReconciled(t *testing.T) {
	log := &opLog{}
	dc := convergeDC(log, convergeMock())
	n := testNames()

	if err := Firewall(context.Background(), dc, nil, cfgWith(masterAndWorker())); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !log.has("firewall:" + n.MasterFirewall()) {
		t.Error("master firewall not reconciled")
	}
	if !log.has("firewall:" + n.WorkerFirewall()) {
		t.Error("worker firewall not reconciled")
	}
}

func TestFirewall_DoesNotDeleteOrphans(t *testing.T) {
	// Firewall() handles the desired set only — orphan sweep is the job of
	// FirewallRemoveOrphans, which runs after ServersRemoveOrphans so the
	// Hetzner "no attached resources" contract is satisfied.
	log := &opLog{}
	dc := convergeDC(log, convergeMock())
	n := testNames()

	activeHetzner.SeedFirewall(n.MasterFirewall())
	activeHetzner.SeedFirewall(n.WorkerFirewall())

	if err := Firewall(context.Background(), dc, nil, cfgWith(masterOnly())); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if log.count("delete-firewall:") != 0 {
		t.Errorf("Firewall must not touch orphans — that's FirewallRemoveOrphans' job; got ops: %v", log.all())
	}
}

func TestFirewall_EmptyFirewallConfig_Skipped(t *testing.T) {
	log := &opLog{}
	dc := convergeDC(log, convergeMock())
	cfg := &config.AppConfig{} // no Firewall, no Servers

	if err := Firewall(context.Background(), dc, nil, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if log.count("firewall:") != 0 {
		t.Errorf("empty firewall config should skip everything, got: %v", log.all())
	}
}

func TestFirewall_Idempotent_NoWorkers(t *testing.T) {
	log := &opLog{}
	dc := convergeDC(log, convergeMock())
	n := testNames()

	_ = Firewall(context.Background(), dc, nil, cfgWith(masterOnly()))
	_ = Firewall(context.Background(), dc, nil, cfgWith(masterOnly()))

	if log.count("firewall:"+n.MasterFirewall()) != 2 {
		t.Errorf("master firewall should reconcile each call: %v", log.all())
	}
	if log.count("firewall:"+n.WorkerFirewall()) != 0 {
		t.Errorf("worker firewall must never fire with no workers: %v", log.all())
	}
}

// ── FirewallRemoveOrphans: orphan sweep ──────────────────────────────────────

func TestFirewallRemoveOrphans_WorkerRemovedFromConfig_OrphanDeleted(t *testing.T) {
	// Seed the provider with a stale WorkerFirewall from a prior deploy
	// that had workers. Now the config has no workers. After ServersRemoveOrphans
	// has run (simulated here — no worker server to begin with), the sweep
	// must delete the stale firewall.
	log := &opLog{}
	dc := convergeDC(log, convergeMock())
	n := testNames()

	activeHetzner.SeedFirewall(n.MasterFirewall())
	activeHetzner.SeedFirewall(n.WorkerFirewall())

	if err := FirewallRemoveOrphans(context.Background(), dc, nil, cfgWith(masterOnly())); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !log.has("delete-firewall:" + n.WorkerFirewall()) {
		t.Errorf("orphan worker firewall must be deleted, got ops: %v", log.all())
	}
	if log.has("delete-firewall:" + n.MasterFirewall()) {
		t.Error("master firewall must NOT be deleted while still desired")
	}
}

func TestFirewallRemoveOrphans_BothRolesDesired_NoDeletion(t *testing.T) {
	// Prior deploy had workers; config still has them. Nothing is orphan,
	// nothing should be swept.
	log := &opLog{}
	dc := convergeDC(log, convergeMock())
	n := testNames()

	activeHetzner.SeedFirewall(n.MasterFirewall())
	activeHetzner.SeedFirewall(n.WorkerFirewall())

	if err := FirewallRemoveOrphans(context.Background(), dc, nil, cfgWith(masterAndWorker())); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if log.count("delete-firewall:") != 0 {
		t.Errorf("no firewalls should be deleted when both roles desired, got: %v", log.all())
	}
}

func TestFirewallRemoveOrphans_UnknownPrefix_NotTouched(t *testing.T) {
	// A firewall NOT matching our nvoi-{app}-{env}- prefix belongs to
	// another app / manual resource — must not be deleted.
	log := &opLog{}
	dc := convergeDC(log, convergeMock())
	n := testNames()

	activeHetzner.SeedFirewall(n.MasterFirewall())
	activeHetzner.SeedFirewall("something-else-fw")

	if err := FirewallRemoveOrphans(context.Background(), dc, nil, cfgWith(masterOnly())); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if log.has("delete-firewall:something-else-fw") {
		t.Error("foreign firewall (outside nvoi prefix) must not be touched")
	}
}

func TestFirewallRemoveOrphans_EmptyFirewallConfig_Skipped(t *testing.T) {
	// No cfg.Firewall → we don't own any per-role firewalls in the first
	// place, so there's nothing to sweep. Must be a no-op.
	log := &opLog{}
	dc := convergeDC(log, convergeMock())
	n := testNames()

	activeHetzner.SeedFirewall(n.WorkerFirewall())

	if err := FirewallRemoveOrphans(context.Background(), dc, nil, &config.AppConfig{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if log.count("delete-firewall:") != 0 {
		t.Errorf("no firewall config → no sweep, got: %v", log.all())
	}
}
