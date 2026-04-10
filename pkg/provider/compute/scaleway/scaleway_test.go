package scaleway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

func testClient(t *testing.T, handler http.Handler) *Client {
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	c := New(map[string]string{
		"secret_key": "test-key",
		"project_id": "test-project",
		"zone":       "fr-par-1",
	})
	c.api.BaseURL = ts.URL
	c.api.HTTPClient = ts.Client()
	return c
}

// ── Firewall rule reconciliation ────────────────────────────────────────────────

func TestEnsureFirewall_ExistingReturnsID(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if r.Method == "GET" && strings.Contains(path, "/security_groups") {
			json.NewEncoder(w).Encode(map[string]any{
				"security_groups": []map[string]any{
					{"id": "sg-123", "name": "nvoi-test-fw"},
				},
			})
			return
		}
		t.Errorf("unexpected request: %s %s — ensureFirewall should only return ID", r.Method, path)
	}))

	id, err := c.ensureFirewall(context.Background(), "nvoi-test-fw", map[string]string{"app": "test"})
	if err != nil {
		t.Fatalf("ensureFirewall: %v", err)
	}
	if id != "sg-123" {
		t.Errorf("id = %q, want %q", id, "sg-123")
	}
}

func TestEnsureFirewall_CreateNew(t *testing.T) {
	created := false
	addedRules := 0
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// GET /security_groups?name=... returns empty — doesn't exist
		if r.Method == "GET" && strings.Contains(path, "/security_groups") && !strings.Contains(path, "/rules") {
			json.NewEncoder(w).Encode(map[string]any{"security_groups": []any{}})
			return
		}
		// POST /security_groups — create new SG
		if r.Method == "POST" && strings.HasSuffix(path, "/security_groups") {
			created = true
			json.NewEncoder(w).Encode(map[string]any{
				"security_group": map[string]any{"id": "sg-new"},
			})
			return
		}
		// POST /security_groups/sg-new/rules — add rules
		if r.Method == "POST" && strings.Contains(path, "/rules") {
			addedRules++
			json.NewEncoder(w).Encode(map[string]any{
				"rule": map[string]any{"id": "rule-new"},
			})
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, path)
	}))

	id, err := c.ensureFirewall(context.Background(), "nvoi-new-fw", map[string]string{"app": "test"})
	if err != nil {
		t.Fatalf("ensureFirewall: %v", err)
	}
	if id != "sg-new" {
		t.Errorf("id = %q, want %q", id, "sg-new")
	}
	if !created {
		t.Error("security group was NOT created")
	}
	if addedRules != 5 {
		t.Errorf("added %d rules on create, want 5", addedRules)
	}
}

func TestBaseFirewallRules_Count(t *testing.T) {
	rules := baseFirewallRules()
	if len(rules) != 5 {
		t.Fatalf("expected 5 base firewall rules, got %d", len(rules))
	}
}

func TestBaseFirewallRules_SSHOpen(t *testing.T) {
	rules := baseFirewallRules()
	found := false
	for _, r := range rules {
		if r.Port == "22" {
			for _, ip := range r.SourceIPs {
				if ip == "0.0.0.0/0" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Error("SSH (22) should be open to 0.0.0.0/0 in base rules")
	}
}

func TestBaseFirewallRules_NoHTTPPorts(t *testing.T) {
	rules := baseFirewallRules()
	for _, r := range rules {
		if r.Port == "80" || r.Port == "443" {
			t.Errorf("port %s should NOT be in base rules — managed by firewall set", r.Port)
		}
	}
}

// ── buildScalewayFirewallRules tests ──────────────────────────────────────────

func TestBuildScalewayFirewallRules_Default(t *testing.T) {
	allowed := provider.PortAllowList{
		"80":  {"0.0.0.0/0"},
		"443": {"0.0.0.0/0"},
	}
	rules := buildScalewayFirewallRules(allowed)

	// Should have internal (4) + SSH (1) + 80 + 443 = 7
	if len(rules) != 7 {
		t.Fatalf("expected 7 rules, got %d", len(rules))
	}

	// Verify 80 and 443 are present with correct CIDRs
	found80, found443 := false, false
	for _, r := range rules {
		if r.Port == "80" {
			found80 = true
			if len(r.SourceIPs) != 1 || r.SourceIPs[0] != "0.0.0.0/0" {
				t.Errorf("port 80 CIDRs = %v, want [0.0.0.0/0]", r.SourceIPs)
			}
		}
		if r.Port == "443" {
			found443 = true
		}
	}
	if !found80 {
		t.Error("expected port 80 in rules")
	}
	if !found443 {
		t.Error("expected port 443 in rules")
	}
}

func TestBuildScalewayFirewallRules_Cloudflare(t *testing.T) {
	cfIPs := []string{"173.245.48.0/20", "103.21.244.0/22"}
	allowed := provider.PortAllowList{
		"80":  cfIPs,
		"443": cfIPs,
	}
	rules := buildScalewayFirewallRules(allowed)

	// SSH should still be open to all (default when not in allow list)
	sshOpen := false
	for _, r := range rules {
		if r.Port == "22" && len(r.SourceIPs) >= 1 {
			for _, ip := range r.SourceIPs {
				if ip == "0.0.0.0/0" {
					sshOpen = true
					break
				}
			}
		}
		if sshOpen {
			sshOpen = true
		}
	}
	if !sshOpen {
		t.Error("SSH should default to 0.0.0.0/0 when not in allow list")
	}

	// Port 80 should have CF IPs
	for _, r := range rules {
		if r.Port == "80" && len(r.SourceIPs) != 2 {
			t.Errorf("port 80 should have 2 CF IPs, got %d", len(r.SourceIPs))
		}
	}
}

func TestBuildScalewayFirewallRules_CloudflareIPv6RetainedInModelButProviderDropsIt(t *testing.T) {
	allowed := provider.PortAllowList{
		"80":  {"173.245.48.0/20", "2400:cb00::/32"},
		"443": {"173.245.48.0/20", "2400:cb00::/32"},
	}
	rules := buildScalewayFirewallRules(allowed)

	foundIPv6InRuleModel := false
	for _, r := range rules {
		if r.Port != "80" && r.Port != "443" {
			continue
		}
		for _, cidr := range r.SourceIPs {
			if strings.Contains(cidr, ":") {
				foundIPv6InRuleModel = true
			}
		}
	}
	if !foundIPv6InRuleModel {
		t.Fatalf("scaleway rule model should preserve requested IPv6 CIDRs before provider application, got %+v", rules)
	}
}

func TestBuildScalewayFirewallRules_SSHOverride(t *testing.T) {
	allowed := provider.PortAllowList{
		"22":  {"10.0.0.1/32"},
		"80":  {"0.0.0.0/0"},
		"443": {"0.0.0.0/0"},
	}
	rules := buildScalewayFirewallRules(allowed)

	for _, r := range rules {
		if r.Port == "22" {
			if len(r.SourceIPs) != 1 || r.SourceIPs[0] != "10.0.0.1/32" {
				t.Errorf("SSH CIDRs = %v, want [10.0.0.1/32]", r.SourceIPs)
			}
		}
	}
}

func TestBuildScalewayFirewallRules_InternalPortsExcluded(t *testing.T) {
	// Even if someone passes internal ports in allow list, they're skipped
	allowed := provider.PortAllowList{
		"6443": {"0.0.0.0/0"},
		"80":   {"0.0.0.0/0"},
	}
	rules := buildScalewayFirewallRules(allowed)

	// Count how many times 6443 appears — should be exactly 1 (from internal rules)
	count6443 := 0
	for _, r := range rules {
		if r.Port == "6443" {
			count6443++
		}
	}
	if count6443 != 1 {
		t.Errorf("port 6443 should appear once (internal only), got %d", count6443)
	}
}

func TestBuildScalewayFirewallRules_CustomPort(t *testing.T) {
	allowed := provider.PortAllowList{
		"80":   {"0.0.0.0/0"},
		"443":  {"0.0.0.0/0"},
		"8080": {"10.0.0.0/8"},
	}
	rules := buildScalewayFirewallRules(allowed)

	found := false
	for _, r := range rules {
		if r.Port == "8080" && len(r.SourceIPs) == 1 && r.SourceIPs[0] == "10.0.0.0/8" {
			found = true
		}
	}
	if !found {
		t.Error("expected custom port 8080 in rules")
	}
}

// ── ReconcileFirewallRules (httptest) ───────────────────────────────────────────

func TestReconcileFirewallRules_Success(t *testing.T) {
	deletedRules := 0
	addedRules := 0

	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// GET /security_groups?name=... returns existing SG
		if r.Method == "GET" && strings.Contains(path, "/security_groups") && !strings.Contains(path, "/rules") {
			json.NewEncoder(w).Encode(map[string]any{
				"security_groups": []map[string]any{
					{"id": "sg-456", "name": "nvoi-test-fw"},
				},
			})
			return
		}
		// GET rules
		if r.Method == "GET" && strings.Contains(path, "/rules") {
			json.NewEncoder(w).Encode(map[string]any{
				"rules": []map[string]any{
					{"id": "old-1"},
					{"id": "old-2"},
					{"id": "old-3"},
				},
			})
			return
		}
		// DELETE rules
		if r.Method == "DELETE" && strings.Contains(path, "/rules/") {
			deletedRules++
			w.WriteHeader(204)
			return
		}
		// POST rules
		if r.Method == "POST" && strings.Contains(path, "/rules") {
			addedRules++
			json.NewEncoder(w).Encode(map[string]any{
				"rule": map[string]any{"id": "new-rule"},
			})
			return
		}
	}))

	allowed := provider.PortAllowList{
		"80":  {"0.0.0.0/0"},
		"443": {"0.0.0.0/0"},
	}
	err := c.ReconcileFirewallRules(context.Background(), "nvoi-test-fw", allowed)
	if err != nil {
		t.Fatalf("ReconcileFirewallRules: %v", err)
	}

	if deletedRules != 3 {
		t.Errorf("deleted %d old rules, want 3", deletedRules)
	}

	// Internal (4) + SSH (1) + 80 + 443 = 7 rules, each with 1 source IP = 7 API calls
	if addedRules != 7 {
		t.Errorf("added %d new rules, want 7", addedRules)
	}
}

func TestReconcileFirewallRules_NotFound(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"security_groups": []any{},
		})
	}))

	err := c.ReconcileFirewallRules(context.Background(), "nonexistent", nil)
	if err == nil {
		t.Fatal("expected ErrNotFound")
	}
}

// ── parsePort ─────────────────────────────────────────────────────────────────

func TestParsePort_Single(t *testing.T) {
	from, to := parsePort("80")
	if from != 80 || to != 0 {
		t.Errorf("parsePort(80) = %d, %d — want 80, 0", from, to)
	}
}

func TestParsePort_Range(t *testing.T) {
	from, to := parsePort("8000-9000")
	if from != 8000 || to != 9000 {
		t.Errorf("parsePort(8000-9000) = %d, %d — want 8000, 9000", from, to)
	}
}

func TestBaseFirewallRules_PrivatePorts(t *testing.T) {
	rules := baseFirewallRules()
	privatePorts := map[string]bool{"6443": false, "10250": false, "5000": false}

	for _, r := range rules {
		if _, ok := privatePorts[r.Port]; ok {
			for _, ip := range r.SourceIPs {
				if ip == utils.PrivateNetworkCIDR {
					privatePorts[r.Port] = true
				}
			}
		}
	}

	for port, found := range privatePorts {
		if !found {
			t.Errorf("port %s should be restricted to %s", port, utils.PrivateNetworkCIDR)
		}
	}
}
