package main

import (
	"context"
	"fmt"
	"testing"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/internal/testutil"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// CLI dispatch contract tests (D4 of refactor #47).
//
// Every nvoi command except `deploy` / `teardown` goes through
// Cluster.Kube or Cluster.SSH, which route to InfraProvider.Connect /
// NodeShell. Connect MUST be read-only: no create-server, no
// firewall-reconcile, no volume-create — drift reconciliation only
// happens via Bootstrap (called exclusively by reconcile.Deploy).
//
// These tests prove the contract by:
//   1. Building a Cluster with ONLY Provider + Credentials + SSHFunc.
//      No MasterKube / NodeShell pre-injection (directive #6 grep
//      gate would catch a regression).
//   2. Pre-seeding the HetznerFake with a master (so findMaster
//      succeeds, Connect reaches the dial step).
//   3. Setting SSHFunc to return an error (so the test doesn't depend
//      on a real SSH or kube tunnel).
//   4. Running the dispatch helper (logs, exec, etc.) and asserting:
//      - It errors at the dial/Connect step (expected).
//      - The HetznerFake OpLog shows the lookup (list-servers:) but
//        ZERO write ops (ensure-server:, ensure-firewall:, etc.).
//
// The "no writes during dispatch" assertion is the load-bearing one.
// It catches the production failure class where `nvoi logs` could
// silently reconcile firewall drift and lock the user out — see D2's
// commit message for the war story.

const dispatchAppName = "myapp"
const dispatchEnvName = "prod"

func dispatchCluster(provName string) app.Cluster {
	return app.Cluster{
		AppName:     dispatchAppName,
		Env:         dispatchEnvName,
		Provider:    provName,
		Credentials: map[string]string{"token": "x"},
		Output:      &testutil.MockOutput{},
		SSHKey:      []byte("dummy"),
		// SSHFunc returns an error — Connect's dial fails before
		// kube.New, but findMaster has already issued list-servers.
		// Letting us assert "lookup happened, no writes happened" without
		// needing a real SSH/kube path.
		SSHFunc: func(ctx context.Context, addr string) (utils.SSHClient, error) {
			return nil, fmt.Errorf("dispatch test — dial intentionally fails")
		},
	}
}

// dispatchSetup registers a HetznerFake under a unique name with one
// pre-seeded master, returns the cluster + a way to inspect ops.
func dispatchSetup(t *testing.T, name string) (app.Cluster, *testutil.HetznerFake) {
	fake := testutil.NewHetznerFake(t)
	fake.SeedServer("nvoi-myapp-prod-master", "1.2.3.4", "10.0.1.1")
	fake.Register(name)
	return dispatchCluster(name), fake
}

// assertNoWrites checks that none of the HetznerFake write ops fired.
// Failure indicates a dispatch command silently mutated provider state —
// the exact bug class the InfraProvider.Connect/Bootstrap split prevents.
func assertNoWrites(t *testing.T, fake *testutil.HetznerFake) {
	t.Helper()
	writeOps := []string{
		"ensure-server:",
		"ensure-firewall:",
		"ensure-volume:",
		"ensure-network:",
		"delete-server:",
		"delete-firewall:",
		"delete-volume:",
		"delete-network:",
		"attach-firewall:",
		"detach-firewall:",
		"detach-volume:",
		"firewall:", // ReconcileFirewallRules op
	}
	for _, prefix := range writeOps {
		if n := fake.Count(prefix); n != 0 {
			t.Errorf("dispatch caused %d write op(s) %q — Connect must be read-only", n, prefix)
		}
	}
}

// assertLookupHappened checks that Connect's findMaster issued at least
// one list-servers query — proves the on-demand path actually ran.
func assertLookupHappened(t *testing.T, fake *testutil.HetznerFake) {
	t.Helper()
	if n := fake.Count("list-servers:"); n == 0 {
		t.Errorf("expected Connect's findMaster to issue list-servers; got %d", n)
	}
}

// minimalDispatchCfg is the smallest AppConfig that satisfies validation
// and gives Connect something to look up. No volumes, no firewall —
// exercising those would let Bootstrap-on-dispatch slip through.
func minimalDispatchCfg(provName string) *config.AppConfig {
	return &config.AppConfig{
		App: dispatchAppName,
		Env: dispatchEnvName,
		Providers: config.ProvidersDef{
			Infra: provName,
		},
		Servers: map[string]config.ServerDef{
			"master": {Type: "cx23", Region: "fsn1", Role: "master"},
		},
		Services: map[string]config.ServiceDef{
			"web": {Image: "nginx", Port: 80},
		},
	}
}

// TestDispatch_Logs_NoWrites locks: `nvoi logs` triggers Connect via
// Cluster.Kube, lookup happens, no provider mutations.
func TestDispatch_Logs_NoWrites(t *testing.T) {
	cluster, fake := dispatchSetup(t, "dispatch-logs")
	cfg := minimalDispatchCfg("dispatch-logs")

	// Logs goes through Cluster.Kube → infra.Connect.
	_ = app.Logs(context.Background(), app.LogsRequest{
		Cluster: cluster,
		Cfg:     config.NewView(cfg),
		Service: "web",
	})

	assertLookupHappened(t, fake)
	assertNoWrites(t, fake)
}

// TestDispatch_Exec_NoWrites locks: `nvoi exec` triggers Connect.
func TestDispatch_Exec_NoWrites(t *testing.T) {
	cluster, fake := dispatchSetup(t, "dispatch-exec")
	cfg := minimalDispatchCfg("dispatch-exec")

	_ = app.Exec(context.Background(), app.ExecRequest{
		Cluster: cluster,
		Cfg:     config.NewView(cfg),
		Service: "web",
		Command: []string{"sh", "-c", "true"},
	})

	assertLookupHappened(t, fake)
	assertNoWrites(t, fake)
}

// TestDispatch_Describe_NoWrites locks: `nvoi describe` triggers Connect.
func TestDispatch_Describe_NoWrites(t *testing.T) {
	cluster, fake := dispatchSetup(t, "dispatch-describe")
	cfg := minimalDispatchCfg("dispatch-describe")

	_, _ = app.Describe(context.Background(), app.DescribeRequest{
		Cluster:      cluster,
		Cfg:          config.NewView(cfg),
		StorageNames: nil,
		Services:     nil,
		Crons:        nil,
	})

	assertLookupHappened(t, fake)
	assertNoWrites(t, fake)
}

// TestDispatch_CronRun_NoWrites locks: `nvoi cron run` triggers Connect.
func TestDispatch_CronRun_NoWrites(t *testing.T) {
	cluster, fake := dispatchSetup(t, "dispatch-cronrun")
	cfg := minimalDispatchCfg("dispatch-cronrun")

	_ = app.CronRun(context.Background(), app.CronRunRequest{
		Cluster: cluster,
		Cfg:     config.NewView(cfg),
		Name:    "cleanup",
	})

	assertLookupHappened(t, fake)
	assertNoWrites(t, fake)
}

// TestDispatch_Resources_NoWrites locks: `nvoi resources` queries the
// provider's ListResources directly (no Connect needed) — should issue
// READ-ONLY list-* calls and zero writes.
func TestDispatch_Resources_NoWrites(t *testing.T) {
	cluster, fake := dispatchSetup(t, "dispatch-resources")

	_, _ = app.Resources(context.Background(), app.ResourcesRequest{
		Infra: app.ProviderRef{Name: cluster.Provider, Creds: cluster.Credentials},
	})

	assertNoWrites(t, fake)
}

// TestDispatch_Resources_IncludesTunnel locks: when providers.tunnel is
// set, `nvoi resources` fans out to TunnelProvider.ListResources and
// includes the returned groups in the output. Regression guard for
// the gap where tunnel resources were silently dropped from the
// resources table.
func TestDispatch_Resources_IncludesTunnel(t *testing.T) {
	cluster, _ := dispatchSetup(t, "dispatch-resources-tunnel")

	tf := testutil.NewCloudflareTunnelFake(t)
	tf.SeedTunnel("tun-1", "nvoi-myapp-prod", "tok")
	tf.Register("dispatch-resources-tunnel-cf")

	groups, err := app.Resources(context.Background(), app.ResourcesRequest{
		Infra:  app.ProviderRef{Name: cluster.Provider, Creds: cluster.Credentials},
		Tunnel: app.ProviderRef{Name: "dispatch-resources-tunnel-cf", Creds: map[string]string{"api_token": "x", "account_id": "y"}},
	})
	if err != nil {
		t.Fatalf("Resources: %v", err)
	}

	var found bool
	var names []string
	for _, g := range groups {
		names = append(names, g.Name)
		if g.Name == "Cloudflare Tunnels" {
			found = true
			if len(g.Rows) != 1 || g.Rows[0][1] != "nvoi-myapp-prod" {
				t.Fatalf("tunnel group rows = %v, want one row for nvoi-myapp-prod", g.Rows)
			}
		}
	}
	if !found {
		t.Fatalf("tunnel group missing from resources output; got groups=%v", names)
	}
}

// TestDispatch_Resources_HardFailsOnProviderError locks the error
// semantics: a failing secondary provider (DNS / Storage / Tunnel) must
// surface as an error from Resources, not be silently swallowed. The
// pre-split code swallowed DNS/Storage errors — diagnostic regressions
// hid behind empty tables.
func TestDispatch_Resources_HardFailsOnProviderError(t *testing.T) {
	_, err := app.Resources(context.Background(), app.ResourcesRequest{
		Tunnel: app.ProviderRef{Name: "not-a-registered-tunnel"},
	})
	if err == nil {
		t.Fatal("expected error when tunnel provider resolves fail; got nil")
	}
}

// TestDispatch_SSH_NoWrites locks: `nvoi ssh` triggers NodeShell via
// Cluster.SSH. NodeShell is read-only by nature (mints a token / dials
// pubkey).
func TestDispatch_SSH_NoWrites(t *testing.T) {
	cluster, fake := dispatchSetup(t, "dispatch-ssh")
	cfg := minimalDispatchCfg("dispatch-ssh")

	_ = app.SSH(context.Background(), app.SSHRequest{
		Cluster: cluster,
		Cfg:     config.NewView(cfg),
		Command: []string{"whoami"},
	})

	assertLookupHappened(t, fake)
	assertNoWrites(t, fake)
}

// TestDispatch_NotBootstrapped_FailsClean locks the "no infra found"
// path: with an empty HetznerFake (no master pre-seeded), Connect's
// findMaster returns provider.ErrNotBootstrapped. Cluster.Kube wraps it
// with "run nvoi deploy first." The dispatch command surfaces this
// actionable error instead of attempting Bootstrap (which would silently
// provision infrastructure on a read command — the bug class D2's
// Connect/Bootstrap split prevents).
func TestDispatch_NotBootstrapped_FailsClean(t *testing.T) {
	provName := "dispatch-empty"
	fake := testutil.NewHetznerFake(t)
	// No SeedServer — fake is empty.
	fake.Register(provName)
	cluster := dispatchCluster(provName)
	cfg := minimalDispatchCfg(provName)

	err := app.Logs(context.Background(), app.LogsRequest{
		Cluster: cluster,
		Cfg:     config.NewView(cfg),
		Service: "web",
	})
	if err == nil {
		t.Fatal("expected error when infra is absent")
	}
	if !contains(err.Error(), "nvoi deploy") {
		t.Errorf("error should point user at `nvoi deploy`, got: %v", err)
	}
	if n := fake.Count("ensure-server:"); n != 0 {
		t.Errorf("dispatch on empty cluster must not create servers; got %d ensure-server ops", n)
	}
}

// TestDispatch_DriftScenario_DoesNotReconcile is the load-bearing one.
// Simulates the production failure that motivated the Connect/Bootstrap
// split: a pre-seeded master exists with manually-modified firewall
// state. Running `nvoi logs` must NOT touch firewalls (no
// reconcileServerFirewall, no attach-firewall, no detach-firewall,
// no firewall: rule-reconcile). If any of those fire, the user just
// got their cluster firewall silently mutated by a read command.
func TestDispatch_DriftScenario_DoesNotReconcile(t *testing.T) {
	provName := "dispatch-drift"
	fake := testutil.NewHetznerFake(t)
	fake.SeedServer("nvoi-myapp-prod-master", "1.2.3.4", "10.0.1.1")
	// Seed an existing firewall — name doesn't match what cfg would
	// produce. If Connect were to call EnsureServer's firewall
	// reconciler, we'd see attach/detach ops in the log.
	fake.SeedFirewall("manually-added-firewall")
	fake.Register(provName)
	cluster := dispatchCluster(provName)
	cfg := minimalDispatchCfg(provName)

	_ = app.Logs(context.Background(), app.LogsRequest{
		Cluster: cluster,
		Cfg:     config.NewView(cfg),
		Service: "web",
	})

	// Specific drift-related write ops MUST be zero — not just the
	// generic "no writes" assertion (which assertNoWrites already
	// covers). Naming them here makes the regression message clear if
	// someone re-introduces drift reconciliation in Connect.
	for _, prefix := range []string{
		"firewall:",        // ReconcileFirewallRules
		"attach-firewall:", // reconcileServerFirewall reattach
		"detach-firewall:", // reconcileServerFirewall detach
		"delete-firewall:", // sweep
	} {
		if n := fake.Count(prefix); n != 0 {
			t.Errorf("nvoi logs caused %d firewall mutation(s) %q — read commands must not reconcile drift", n, prefix)
		}
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
