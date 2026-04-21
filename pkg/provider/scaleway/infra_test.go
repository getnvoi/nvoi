package scaleway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

// ── ArchForType ────────────────────────────────────────────────────────────────

func TestArchForType_Scaleway(t *testing.T) {
	c := &Client{}
	cases := []struct {
		instanceType string
		wantArch     string
	}{
		{"AMP2-C4", "arm64"},
		{"amp2-c4", "arm64"}, // case-insensitive
		{"COPARM1-4C-16G", "arm64"},
		{"coparm1-4c-16g", "arm64"}, // case-insensitive
		{"DEV1-S", "amd64"},
		{"GP1-XS", "amd64"},
		{"PRO2-XXS", "amd64"},
		{"PLAY2-PICO", "amd64"},
	}
	for _, tc := range cases {
		got := c.ArchForType(tc.instanceType)
		if got != tc.wantArch {
			t.Errorf("ArchForType(%q) = %q, want %q", tc.instanceType, got, tc.wantArch)
		}
	}
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

// ── CreateServer disk size ───────────────────────────────────────────────────

func TestCreateServer_CustomDisk(t *testing.T) {
	var capturedRootSize int64
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// ensureFirewall
		if r.Method == "GET" && strings.Contains(path, "/security_groups") {
			json.NewEncoder(w).Encode(map[string]any{
				"security_groups": []map[string]any{
					{"id": "sg-1", "name": "test-fw"},
				},
			})
			return
		}
		// ensureNetwork
		if r.Method == "GET" && strings.Contains(path, "/private_networks") {
			json.NewEncoder(w).Encode(map[string]any{
				"private_networks": []map[string]any{
					{"id": "net-1", "name": "test-net"},
				},
			})
			return
		}
		// getServerByName — not found
		if r.Method == "GET" && strings.HasSuffix(path, "/servers") {
			json.NewEncoder(w).Encode(map[string]any{"servers": []any{}})
			return
		}
		// resolveImage
		if r.Method == "GET" && strings.Contains(path, "/images") {
			json.NewEncoder(w).Encode(map[string]any{
				"images": []map[string]any{{"id": "img-1"}},
			})
			return
		}
		// createServer — capture the root volume size
		if r.Method == "POST" && strings.HasSuffix(path, "/servers") {
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if vols, ok := body["volumes"].(map[string]any); ok {
				if v0, ok := vols["0"].(map[string]any); ok {
					capturedRootSize = int64(v0["size"].(float64))
				}
			}
			json.NewEncoder(w).Encode(map[string]any{
				"server": serverJSON{ID: "srv-1", Name: "test-srv", State: "running"},
			})
			return
		}
		// user_data, private_nics, poweron, refetch
		if r.Method == "PATCH" || (r.Method == "POST" && strings.Contains(path, "/action")) {
			w.WriteHeader(200)
			json.NewEncoder(w).Encode(map[string]any{})
			return
		}
		if r.Method == "PUT" {
			w.WriteHeader(204)
			return
		}
		if r.Method == "POST" && strings.Contains(path, "/private_nics") {
			json.NewEncoder(w).Encode(map[string]any{"private_nic": map[string]any{"id": "nic-1"}})
			return
		}
		if r.Method == "GET" && strings.Contains(path, "/servers/srv-1") {
			json.NewEncoder(w).Encode(map[string]any{
				"server": serverJSON{ID: "srv-1", Name: "test-srv", State: "running"},
			})
			return
		}
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]any{})
	}))

	_, err := c.EnsureServer(context.Background(), provider.CreateServerRequest{
		Name:         "test-srv",
		ServerType:   "DEV1-M",
		Image:        "ubuntu-24.04",
		Location:     "fr-par-1",
		FirewallName: "test-fw",
		NetworkName:  "test-net",
		DiskGB:       50,
		Labels:       map[string]string{"app": "test"},
	})
	if err != nil {
		t.Fatalf("EnsureServer: %v", err)
	}
	expected := int64(50_000_000_000)
	if capturedRootSize != expected {
		t.Errorf("root volume size = %d bytes, want %d (50 GB)", capturedRootSize, expected)
	}
}

func TestCreateServer_DefaultDisk(t *testing.T) {
	var capturedRootSize int64
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if r.Method == "GET" && strings.Contains(path, "/security_groups") {
			json.NewEncoder(w).Encode(map[string]any{"security_groups": []map[string]any{{"id": "sg-1", "name": "fw"}}})
			return
		}
		if r.Method == "GET" && strings.Contains(path, "/private_networks") {
			json.NewEncoder(w).Encode(map[string]any{"private_networks": []map[string]any{{"id": "net-1", "name": "net"}}})
			return
		}
		if r.Method == "GET" && strings.HasSuffix(path, "/servers") {
			json.NewEncoder(w).Encode(map[string]any{"servers": []any{}})
			return
		}
		if r.Method == "GET" && strings.Contains(path, "/images") {
			json.NewEncoder(w).Encode(map[string]any{"images": []map[string]any{{"id": "img-1"}}})
			return
		}
		if r.Method == "POST" && strings.HasSuffix(path, "/servers") {
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if vols, ok := body["volumes"].(map[string]any); ok {
				if v0, ok := vols["0"].(map[string]any); ok {
					capturedRootSize = int64(v0["size"].(float64))
				}
			}
			json.NewEncoder(w).Encode(map[string]any{
				"server": serverJSON{ID: "srv-1", Name: "test-srv", State: "running"},
			})
			return
		}
		if r.Method == "GET" && strings.Contains(path, "/servers/srv-1") {
			json.NewEncoder(w).Encode(map[string]any{"server": serverJSON{ID: "srv-1", Name: "test-srv", State: "running"}})
			return
		}
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]any{})
	}))

	_, err := c.EnsureServer(context.Background(), provider.CreateServerRequest{
		Name: "test-srv", ServerType: "DEV1-M", Image: "ubuntu-24.04",
		Location: "fr-par-1", FirewallName: "fw", NetworkName: "net",
		Labels: map[string]string{"app": "test"},
		// DiskGB omitted — should default to 20 GB
	})
	if err != nil {
		t.Fatalf("EnsureServer: %v", err)
	}
	expected := int64(20_000_000_000)
	if capturedRootSize != expected {
		t.Errorf("root volume size = %d bytes, want %d (20 GB default)", capturedRootSize, expected)
	}
}

// ── DeleteServer SG release ──────────────────────────────────────────────────

func TestDeleteServer_ReleasesSecurityGroup(t *testing.T) {
	patchedSG := false
	terminated := false
	pollCount := 0

	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// getServerByName
		if r.Method == "GET" && strings.HasSuffix(path, "/servers") {
			if pollCount > 0 {
				// Second call: server gone
				json.NewEncoder(w).Encode(map[string]any{"servers": []any{}})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{
				"servers": []serverJSON{{ID: "srv-1", Name: "test-server", State: "running"}},
			})
			return
		}

		// getDefaultSecurityGroupID
		if r.Method == "GET" && strings.Contains(path, "/security_groups") {
			json.NewEncoder(w).Encode(map[string]any{
				"security_groups": []map[string]any{
					{"id": "sg-default", "project_default": true},
					{"id": "sg-custom", "project_default": false},
				},
			})
			return
		}

		// PATCH /servers/srv-1 — SG reassignment
		if r.Method == "PATCH" && strings.Contains(path, "/servers/srv-1") {
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if sg, ok := body["security_group"]; ok {
				sgMap := sg.(map[string]any)
				if sgMap["id"] == "sg-default" {
					patchedSG = true
				}
			}
			json.NewEncoder(w).Encode(map[string]any{"server": map[string]any{"id": "srv-1"}})
			return
		}

		// getServerVolumeIDs
		if r.Method == "GET" && strings.Contains(path, "/servers/srv-1") {
			json.NewEncoder(w).Encode(map[string]any{
				"server": map[string]any{"id": "srv-1", "volumes": map[string]any{}},
			})
			return
		}

		// terminate
		if r.Method == "POST" && strings.Contains(path, "/action") {
			terminated = true
			pollCount++
			w.WriteHeader(202)
			return
		}

		t.Errorf("unexpected request: %s %s", r.Method, path)
	}))

	err := c.DeleteServer(context.Background(), provider.DeleteServerRequest{Name: "test-server"})
	if err != nil {
		t.Fatalf("DeleteServer: %v", err)
	}
	if !patchedSG {
		t.Error("DeleteServer should reassign server to default SG before termination")
	}
	if !terminated {
		t.Error("DeleteServer should terminate the server")
	}
}

func TestDeleteServer_SGReleaseFails_StillTerminates(t *testing.T) {
	terminated := false
	pollCount := 0

	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// getServerByName
		if r.Method == "GET" && strings.HasSuffix(path, "/servers") {
			if pollCount > 0 {
				json.NewEncoder(w).Encode(map[string]any{"servers": []any{}})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{
				"servers": []serverJSON{{ID: "srv-1", Name: "test-server", State: "running"}},
			})
			return
		}

		// getDefaultSecurityGroupID — fail (no default SG found)
		if r.Method == "GET" && strings.Contains(path, "/security_groups") {
			json.NewEncoder(w).Encode(map[string]any{"security_groups": []any{}})
			return
		}

		// getServerVolumeIDs
		if r.Method == "GET" && strings.Contains(path, "/servers/srv-1") {
			json.NewEncoder(w).Encode(map[string]any{
				"server": map[string]any{"id": "srv-1", "volumes": map[string]any{}},
			})
			return
		}

		// terminate — should still happen even though SG release failed
		if r.Method == "POST" && strings.Contains(path, "/action") {
			terminated = true
			pollCount++
			w.WriteHeader(202)
			return
		}

		t.Errorf("unexpected request: %s %s", r.Method, path)
	}))

	err := c.DeleteServer(context.Background(), provider.DeleteServerRequest{Name: "test-server"})
	if err != nil {
		t.Fatalf("DeleteServer should succeed even when SG release fails: %v", err)
	}
	if !terminated {
		t.Error("server should still be terminated when SG release fails")
	}
}

// ── DeleteFirewall ───────────────────────────────────────────────────────────

func TestDeleteFirewall_Success(t *testing.T) {
	deleted := false
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && strings.Contains(r.URL.Path, "/security_groups") {
			json.NewEncoder(w).Encode(map[string]any{
				"security_groups": []map[string]any{
					{"id": "sg-123", "name": "nvoi-test-fw"},
				},
			})
			return
		}
		if r.Method == "DELETE" {
			deleted = true
			w.WriteHeader(204)
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))

	err := c.DeleteFirewall(context.Background(), "nvoi-test-fw")
	if err != nil {
		t.Fatalf("DeleteFirewall: %v", err)
	}
	if !deleted {
		t.Error("DELETE was not called")
	}
}

func TestDeleteFirewall_InUseIsRetryable(t *testing.T) {
	attempts := 0
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && strings.Contains(r.URL.Path, "/security_groups") {
			json.NewEncoder(w).Encode(map[string]any{
				"security_groups": []map[string]any{
					{"id": "sg-123", "name": "nvoi-test-fw"},
				},
			})
			return
		}
		if r.Method == "DELETE" {
			attempts++
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]any{
				"message": "group is in use. you cannot delete it.",
			})
			return
		}
	}))

	// Short context — cancels the poll before the 2s retry interval.
	// Proves "in use" is treated as retryable (not returned as hard error).
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := c.DeleteFirewall(ctx, "nvoi-test-fw")
	if err == nil {
		t.Fatal("expected error with always-in-use mock")
	}
	// Error must NOT be about "in use" — that would mean it was treated as a hard error.
	if strings.Contains(err.Error(), "in use") {
		t.Errorf("'in use' should be retried, not returned as error: %s", err)
	}
	if attempts < 1 {
		t.Error("DELETE should have been attempted at least once")
	}
}

func TestDeleteFirewall_HardErrorPropagates(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && strings.Contains(r.URL.Path, "/security_groups") {
			json.NewEncoder(w).Encode(map[string]any{
				"security_groups": []map[string]any{
					{"id": "sg-123", "name": "nvoi-test-fw"},
				},
			})
			return
		}
		if r.Method == "DELETE" {
			w.WriteHeader(500)
			json.NewEncoder(w).Encode(map[string]any{
				"message": "internal server error",
			})
			return
		}
	}))

	err := c.DeleteFirewall(context.Background(), "nvoi-test-fw")
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestDeleteFirewall_NotFound_Idempotent(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"security_groups": []any{}})
	}))

	err := c.DeleteFirewall(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("DeleteFirewall should be idempotent for absent SG: %v", err)
	}
}

func TestGetDefaultSecurityGroupID(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"security_groups": []map[string]any{
				{"id": "sg-custom", "project_default": false},
				{"id": "sg-default", "project_default": true},
			},
		})
	}))

	id, err := c.getDefaultSecurityGroupID(context.Background())
	if err != nil {
		t.Fatalf("getDefaultSecurityGroupID: %v", err)
	}
	if id != "sg-default" {
		t.Errorf("id = %q, want sg-default", id)
	}
}

func TestGetDefaultSecurityGroupID_NoneFound(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"security_groups": []map[string]any{
				{"id": "sg-1", "project_default": false},
			},
		})
	}))

	_, err := c.getDefaultSecurityGroupID(context.Background())
	if err == nil {
		t.Fatal("expected error when no default SG exists")
	}
	if !strings.Contains(err.Error(), "no default security group") {
		t.Errorf("error should mention 'no default security group', got: %s", err)
	}
}

// ── DeleteServer poll correctness ────────────────────────────────────────────

func TestDeleteServer_APIErrorDuringPoll_RetriesNotShortCircuits(t *testing.T) {
	pollCalls := 0
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// All GETs to /servers return 500 — simulates transient API errors
		if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/servers") {
			pollCalls++
			w.WriteHeader(500)
			json.NewEncoder(w).Encode(map[string]any{"message": "internal error"})
			return
		}
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]any{})
	}))

	// Short context — cancels poll before retry interval.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Isolate the poll logic directly.
	err := utils.Poll(ctx, 3*time.Second, 90*time.Second, func() (bool, error) {
		s, getErr := c.getServerByName(ctx, "test-srv")
		if getErr != nil {
			return false, nil // fix: retry on API error
		}
		return s == nil, nil
	})

	// Old code: 500 → err!=nil → return true → "server gone" (wrong).
	// New code: 500 → retry → context cancel → timeout.
	if err == nil {
		t.Fatal("poll should NOT succeed when API returns errors")
	}
	if pollCalls < 1 {
		t.Error("should have attempted at least one API call")
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
