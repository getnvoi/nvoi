package scaleway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

func TestEnsureFirewall_ReconcileExistingRules(t *testing.T) {
	deletedRules := 0
	addedRules := 0
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// GET /security_groups?name=... returns existing SG
		if r.Method == "GET" && strings.Contains(path, "/security_groups") && !strings.Contains(path, "/rules") {
			json.NewEncoder(w).Encode(map[string]any{
				"security_groups": []map[string]any{
					{"id": "sg-123", "name": "nvoi-test-fw"},
				},
			})
			return
		}
		// GET /security_groups/sg-123/rules — list existing rules
		if r.Method == "GET" && strings.Contains(path, "/rules") {
			json.NewEncoder(w).Encode(map[string]any{
				"rules": []map[string]any{
					{"id": "rule-old-1"},
					{"id": "rule-old-2"},
				},
			})
			return
		}
		// DELETE /security_groups/sg-123/rules/rule-old-* — delete old rules
		if r.Method == "DELETE" && strings.Contains(path, "/rules/") {
			deletedRules++
			w.WriteHeader(204)
			return
		}
		// POST /security_groups/sg-123/rules — add new rules
		if r.Method == "POST" && strings.Contains(path, "/rules") {
			addedRules++
			json.NewEncoder(w).Encode(map[string]any{
				"rule": map[string]any{"id": "rule-new"},
			})
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, path)
	}))

	id, err := c.ensureFirewall(context.Background(), "nvoi-test-fw", map[string]string{"app": "test"})
	if err != nil {
		t.Fatalf("ensureFirewall: %v", err)
	}
	if id != "sg-123" {
		t.Errorf("id = %q, want %q", id, "sg-123")
	}
	if deletedRules != 2 {
		t.Errorf("deleted %d old rules, want 2", deletedRules)
	}
	// baseFirewallRules returns 5 rules (SSH + 4 internal, no HTTP).
	// Each rule has 1 source IP → 5 addSGRule calls.
	if addedRules != 5 {
		t.Errorf("added %d new rules, want 5", addedRules)
	}
}

func TestEnsureFirewall_CreateNew(t *testing.T) {
	created := false
	addedRules := 0
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// GET /security_groups?name=... returns empty ��� doesn't exist
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
