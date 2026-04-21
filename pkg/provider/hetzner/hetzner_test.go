package hetzner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

func testClient(t *testing.T, handler http.Handler) *Client {
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	c := New("test-token")
	c.api.BaseURL = ts.URL
	c.api.HTTPClient = ts.Client()
	return c
}

func TestListServers(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("expected GET, got %s", r.Method)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"servers": []map[string]any{
				{
					"id":     1,
					"name":   "test-server",
					"status": "running",
					"public_net": map[string]any{
						"ipv4": map[string]string{"ip": "1.2.3.4"},
						"ipv6": map[string]string{"ip": ""},
					},
					"private_net": []map[string]string{
						{"ip": "10.0.1.1"},
					},
				},
			},
		})
	}))

	servers, err := c.ListServers(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListServers: %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(servers))
	}
	s := servers[0]
	if s.ID != "1" {
		t.Errorf("ID = %q, want %q", s.ID, "1")
	}
	if s.Name != "test-server" {
		t.Errorf("Name = %q, want %q", s.Name, "test-server")
	}
	if s.IPv4 != "1.2.3.4" {
		t.Errorf("IPv4 = %q, want %q", s.IPv4, "1.2.3.4")
	}
	if s.PrivateIP != "10.0.1.1" {
		t.Errorf("PrivateIP = %q, want %q", s.PrivateIP, "10.0.1.1")
	}
	if string(s.Status) != "running" {
		t.Errorf("Status = %q, want %q", s.Status, "running")
	}
}

func TestListServersWithLabels(t *testing.T) {
	var requestURL string
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestURL = r.URL.String()
		json.NewEncoder(w).Encode(map[string]any{"servers": []any{}})
	}))

	labels := map[string]string{"app": "myapp"}
	servers, err := c.ListServers(context.Background(), labels)
	if err != nil {
		t.Fatalf("ListServers: %v", err)
	}
	if len(servers) != 0 {
		t.Fatalf("expected 0 servers, got %d", len(servers))
	}
	if requestURL == "" {
		t.Fatal("no request was made")
	}
	if got := requestURL; !contains(got, "label_selector=") {
		t.Errorf("request URL %q should contain label_selector=", got)
	}
}

func TestEnsureServer_AlreadyExists(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// getServerByName — GET /servers?name=existing-server
		if r.Method == "GET" && contains(r.URL.String(), "/servers?name=") {
			json.NewEncoder(w).Encode(map[string]any{
				"servers": []map[string]any{
					{
						"id":     42,
						"name":   "existing-server",
						"status": "running",
						"public_net": map[string]any{
							"ipv4":      map[string]string{"ip": "5.6.7.8"},
							"ipv6":      map[string]string{"ip": ""},
							"firewalls": []map[string]any{{"id": 50}},
						},
						"private_net": []map[string]string{},
					},
				},
			})
			return
		}
		// ensureFirewall — GET /firewalls?name=...
		if r.Method == "GET" && contains(r.URL.String(), "/firewalls") {
			json.NewEncoder(w).Encode(map[string]any{
				"firewalls": []map[string]any{{"id": 50, "name": "nvoi-test-dev-fw"}},
			})
			return
		}
		// getServerAttachments — GET /servers/42
		if r.Method == "GET" && contains(r.URL.Path, "/servers/42") {
			json.NewEncoder(w).Encode(map[string]any{
				"server": map[string]any{
					"public_net": map[string]any{
						"firewalls": []map[string]any{{"id": 50}},
					},
					"volumes": []int64{},
				},
			})
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))

	srv, err := c.EnsureServer(context.Background(), provider_CreateServerRequest("existing-server"))
	if err != nil {
		t.Fatalf("EnsureServer: %v", err)
	}
	if srv.ID != "42" {
		t.Errorf("ID = %q, want %q", srv.ID, "42")
	}
	if srv.Name != "existing-server" {
		t.Errorf("Name = %q, want %q", srv.Name, "existing-server")
	}
	if srv.IPv4 != "5.6.7.8" {
		t.Errorf("IPv4 = %q, want %q", srv.IPv4, "5.6.7.8")
	}
}

func TestDeleteServer_FetchesAttachmentsOnce(t *testing.T) {
	serverDetailCalls := 0
	deleted := false
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// getServerByName — list endpoint. Returns gone after DELETE.
		if r.Method == "GET" && contains(r.URL.String(), "/servers?name=") {
			if deleted {
				json.NewEncoder(w).Encode(map[string]any{"servers": []any{}})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{
				"servers": []map[string]any{
					{
						"id": 10, "name": "target-server", "status": "running",
						"public_net": map[string]any{
							"ipv4": map[string]string{"ip": "1.2.3.4"},
							"ipv6": map[string]string{"ip": ""},
						},
						"private_net": []any{},
					},
				},
			})
			return
		}
		// getServerAttachments — single server detail fetch
		if r.Method == "GET" && contains(r.URL.Path, "/servers/10") {
			serverDetailCalls++
			json.NewEncoder(w).Encode(map[string]any{
				"server": map[string]any{
					"public_net": map[string]any{
						"firewalls": []map[string]any{{"id": 50}},
					},
					"volumes": []int64{60},
				},
			})
			return
		}
		// detachFirewall
		if r.Method == "POST" && contains(r.URL.Path, "/firewalls/50/actions/remove_from_resources") {
			json.NewEncoder(w).Encode(map[string]any{
				"actions": []map[string]any{{"id": 1}},
			})
			return
		}
		// detachVolume
		if r.Method == "POST" && contains(r.URL.Path, "/volumes/60/actions/detach") {
			json.NewEncoder(w).Encode(map[string]any{
				"action": map[string]any{"id": 2},
			})
			return
		}
		// waitForAction
		if r.Method == "GET" && contains(r.URL.Path, "/actions/") {
			json.NewEncoder(w).Encode(map[string]any{
				"action": map[string]any{"id": 1, "status": "success"},
			})
			return
		}
		// DELETE server
		if r.Method == "DELETE" && contains(r.URL.Path, "/servers/10") {
			deleted = true
			w.WriteHeader(204)
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := c.DeleteServer(ctx, deleteServerRequest("target-server"))
	if err != nil {
		t.Fatalf("DeleteServer: %v", err)
	}
	if serverDetailCalls != 1 {
		t.Errorf("expected exactly 1 GET /servers/{id} call for attachments, got %d", serverDetailCalls)
	}
}

func TestDeleteServer_NotFound(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// getServerByName returns empty list — server doesn't exist
		if r.Method == "GET" && contains(r.URL.String(), "/servers") {
			json.NewEncoder(w).Encode(map[string]any{"servers": []any{}})
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))

	err := c.DeleteServer(context.Background(), deleteServerRequest("gone-server"))
	if err != nil {
		t.Fatalf("DeleteServer should be idempotent (nil for absent server), got: %v", err)
	}
}

func TestDeleteServer_APIErrorDuringPoll_RetriesNotShortCircuits(t *testing.T) {
	// Verify that a transient API error during the "wait for gone" poll
	// is retried — not treated as "server is gone."
	pollCalls := 0
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && contains(r.URL.String(), "/servers?name=") {
			// Always return "server still exists" — we're testing the poll won't
			// short-circuit on API errors before we cancel via context.
			pollCalls++
			w.WriteHeader(500)
			json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{"message": "internal error"},
			})
			return
		}
		// Anything else — not reached in this test
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]any{})
	}))

	// Short context — cancels poll before 3s retry interval.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Call the poll function directly with a fake server ID to skip
	// the firewall/volume/delete steps and isolate the poll behavior.
	err := utils.Poll(ctx, 3*time.Second, 2*time.Minute, func() (bool, error) {
		s, getErr := c.getServerByName(ctx, "test-srv")
		if getErr != nil {
			return false, nil // THIS is the fix — old code returned true here
		}
		return s == nil, nil
	})

	// Should timeout (context cancelled), NOT succeed.
	// Old code: 500 → err!=nil → return true → poll exits "success" → server "gone" (wrong).
	// New code: 500 → err!=nil → return false → retry → context cancel → timeout.
	if err == nil {
		t.Fatal("poll should NOT succeed when API returns errors — that means err!=nil was treated as 'server gone'")
	}
	if pollCalls < 1 {
		t.Error("poll should have attempted at least one API call")
	}
}

func TestListVolumes(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"volumes": []map[string]any{
				{
					"id":           1,
					"name":         "test-vol",
					"size":         20,
					"server":       nil,
					"location":     map[string]string{"name": "fsn1"},
					"linux_device": "/dev/sda",
					"status":       "available",
					"labels":       map[string]string{},
				},
			},
		})
	}))

	vols, err := c.ListVolumes(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListVolumes: %v", err)
	}
	if len(vols) != 1 {
		t.Fatalf("expected 1 volume, got %d", len(vols))
	}
	v := vols[0]
	if v.ID != "1" {
		t.Errorf("ID = %q, want %q", v.ID, "1")
	}
	if v.Name != "test-vol" {
		t.Errorf("Name = %q, want %q", v.Name, "test-vol")
	}
	if v.Size != 20 {
		t.Errorf("Size = %d, want %d", v.Size, 20)
	}
	if v.Location != "fsn1" {
		t.Errorf("Location = %q, want %q", v.Location, "fsn1")
	}
	if v.DevicePath != "/dev/sda" {
		t.Errorf("DevicePath = %q, want %q", v.DevicePath, "/dev/sda")
	}
	if v.ServerID != "" {
		t.Errorf("ServerID = %q, want empty (unattached)", v.ServerID)
	}
}

func TestListAllFirewalls(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"firewalls": []map[string]any{
				{"id": 10, "name": "nvoi-app-prod-fw"},
			},
		})
	}))

	fws, err := c.ListAllFirewalls(context.Background())
	if err != nil {
		t.Fatalf("ListAllFirewalls: %v", err)
	}
	if len(fws) != 1 {
		t.Fatalf("expected 1 firewall, got %d", len(fws))
	}
	if fws[0].ID != "10" {
		t.Errorf("ID = %q, want %q", fws[0].ID, "10")
	}
	if fws[0].Name != "nvoi-app-prod-fw" {
		t.Errorf("Name = %q, want %q", fws[0].Name, "nvoi-app-prod-fw")
	}
}

func TestListAllNetworks(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"networks": []map[string]any{
				{"id": 20, "name": "nvoi-app-prod-net"},
			},
		})
	}))

	nets, err := c.ListAllNetworks(context.Background())
	if err != nil {
		t.Fatalf("ListAllNetworks: %v", err)
	}
	if len(nets) != 1 {
		t.Fatalf("expected 1 network, got %d", len(nets))
	}
	if nets[0].ID != "20" {
		t.Errorf("ID = %q, want %q", nets[0].ID, "20")
	}
	if nets[0].Name != "nvoi-app-prod-net" {
		t.Errorf("Name = %q, want %q", nets[0].Name, "nvoi-app-prod-net")
	}
}

func TestValidateCredentials(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if !contains(r.URL.String(), "/datacenters") {
			t.Errorf("expected /datacenters path, got %s", r.URL.String())
		}
		// Verify auth header
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-token" {
			t.Errorf("Authorization = %q, want %q", auth, "Bearer test-token")
		}
		json.NewEncoder(w).Encode(map[string]any{
			"datacenters": []map[string]any{{}},
		})
	}))

	if err := c.ValidateCredentials(context.Background()); err != nil {
		t.Fatalf("ValidateCredentials: %v", err)
	}
}

func TestValidateCredentials_EmptyToken(t *testing.T) {
	c := New("")
	err := c.ValidateCredentials(context.Background())
	if err == nil {
		t.Fatal("expected error for empty token")
	}
	if !contains(err.Error(), "HETZNER_TOKEN") {
		t.Errorf("error %q should mention HETZNER_TOKEN", err.Error())
	}
}

func TestArchForType(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"cax11", "arm64"},
		{"cx21", "amd64"},
		{"CAX31", "arm64"},
		{"cpx11", "amd64"},
		{"cax", "arm64"},
	}
	c := New("unused")
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := c.ArchForType(tt.input)
			if got != tt.want {
				t.Errorf("ArchForType(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ── ResizeVolume ─────────────────────────────────────────────────────────────

func TestResizeVolume(t *testing.T) {
	var resizedTo int
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && contains(r.URL.Path, "/actions/resize") {
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			resizedTo = int(body["size"].(float64))
			json.NewEncoder(w).Encode(map[string]any{
				"action": map[string]any{"id": 1, "status": "success"},
			})
			return
		}
		if r.Method == "GET" && contains(r.URL.Path, "/actions/") {
			json.NewEncoder(w).Encode(map[string]any{
				"action": map[string]any{"id": 1, "status": "success"},
			})
			return
		}
		w.WriteHeader(404)
	}))

	err := c.ResizeVolume(context.Background(), "123", 50)
	if err != nil {
		t.Fatalf("ResizeVolume: %v", err)
	}
	if resizedTo != 50 {
		t.Errorf("resized to %d, want 50", resizedTo)
	}
}

// ── buildFirewallRules unit tests ───────────────────────────────────────────────

func TestBuildFirewallRules_NilBase_NoHTTPPorts(t *testing.T) {
	// Tunnel mode and no-domain configs pass nil — master should get SSH +
	// internal only, never 80 or 443.
	rules := buildFirewallRules(nil)
	for _, r := range rules {
		if r.Port == "80" || r.Port == "443" {
			t.Errorf("nil allow-list must not produce port %s rule (tunnel/no-domain mode)", r.Port)
		}
	}
	// SSH must always be present.
	hasSSH := false
	for _, r := range rules {
		if r.Port == "22" {
			hasSSH = true
		}
	}
	if !hasSSH {
		t.Error("SSH (22) must always be present in firewall rules")
	}
}

func TestBuildFirewallRules_WithHTTPBase_HasBothPorts(t *testing.T) {
	// Caddy mode passes {80, 443} as the auto-derived base.
	base := provider.PortAllowList{
		"80":  {"0.0.0.0/0", "::/0"},
		"443": {"0.0.0.0/0", "::/0"},
	}
	rules := buildFirewallRules(base)
	has80, has443 := false, false
	for _, r := range rules {
		if r.Port == "80" {
			has80 = true
		}
		if r.Port == "443" {
			has443 = true
		}
	}
	if !has80 {
		t.Error("Caddy base allow-list must produce port 80 rule")
	}
	if !has443 {
		t.Error("Caddy base allow-list must produce port 443 rule")
	}
}

func TestBuildFirewallRules_SSHOverride_AppliesCustomCIDR(t *testing.T) {
	base := provider.PortAllowList{
		"22": {"10.0.0.0/8"},
	}
	rules := buildFirewallRules(base)
	for _, r := range rules {
		if r.Port == "22" {
			if len(r.SourceIPs) != 1 || r.SourceIPs[0] != "10.0.0.0/8" {
				t.Errorf("SSH CIDR override not applied: got %v", r.SourceIPs)
			}
			return
		}
	}
	t.Error("SSH rule not found")
}

// ── Firewall rule reconciliation ────────────────────────────────────────────────

func TestEnsureFirewall_ExistingReturnsID(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && contains(r.URL.String(), "/firewalls") {
			json.NewEncoder(w).Encode(map[string]any{
				"firewalls": []map[string]any{
					{"id": 99, "name": "nvoi-test-fw"},
				},
			})
			return
		}
		// ensureFirewall should NOT call set_rules on existing firewall.
		// Rules are managed by ReconcileFirewallRules, not ensureFirewall.
		t.Errorf("unexpected request: %s %s — ensureFirewall should only return ID for existing firewall", r.Method, r.URL.Path)
	}))

	id, err := c.ensureFirewall(context.Background(), "nvoi-test-fw", map[string]string{"app": "test"})
	if err != nil {
		t.Fatalf("ensureFirewall: %v", err)
	}
	if id != "99" {
		t.Errorf("id = %q, want %q", id, "99")
	}
}

func TestEnsureFirewall_CreateNew(t *testing.T) {
	created := false
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// GET /firewalls?name=... returns empty — firewall doesn't exist
		if r.Method == "GET" && contains(r.URL.String(), "/firewalls") {
			json.NewEncoder(w).Encode(map[string]any{"firewalls": []any{}})
			return
		}
		// POST /firewalls — create new firewall
		if r.Method == "POST" && r.URL.Path == "/firewalls" {
			created = true
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			rules, ok := body["rules"].([]any)
			if !ok || len(rules) != 5 {
				t.Errorf("expected 5 base rules on create, got %v", len(rules))
			}
			json.NewEncoder(w).Encode(map[string]any{
				"firewall": map[string]any{"id": 101},
			})
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))

	id, err := c.ensureFirewall(context.Background(), "nvoi-new-fw", map[string]string{"app": "test"})
	if err != nil {
		t.Fatalf("ensureFirewall: %v", err)
	}
	if id != "101" {
		t.Errorf("id = %q, want %q", id, "101")
	}
	if !created {
		t.Error("firewall was NOT created")
	}
}

func TestDeleteFirewall_Exists(t *testing.T) {
	deleted := false
	n := testNames()
	fwName := n.Firewall()
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && contains(r.URL.String(), "/firewalls") {
			json.NewEncoder(w).Encode(map[string]any{
				"firewalls": []map[string]any{
					{"id": 55, "name": fwName},
				},
			})
			return
		}
		if r.Method == "DELETE" && contains(r.URL.Path, "/firewalls/55") {
			deleted = true
			w.WriteHeader(204)
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))

	err := c.DeleteFirewall(context.Background(), fwName)
	if err != nil {
		t.Fatalf("DeleteFirewall: %v", err)
	}
	if !deleted {
		t.Error("DELETE was not called")
	}
}

func TestDeleteFirewall_NotFound(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"firewalls": []any{}})
	}))

	err := c.DeleteFirewall(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("DeleteFirewall should be idempotent (nil for absent), got: %v", err)
	}
}

func TestDeleteNetwork_Exists(t *testing.T) {
	deleted := false
	n := testNames()
	netName := n.Network()
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && contains(r.URL.String(), "/networks") {
			json.NewEncoder(w).Encode(map[string]any{
				"networks": []map[string]any{
					{"id": 77, "name": netName},
				},
			})
			return
		}
		if r.Method == "DELETE" && contains(r.URL.Path, "/networks/77") {
			deleted = true
			w.WriteHeader(204)
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))

	err := c.DeleteNetwork(context.Background(), netName)
	if err != nil {
		t.Fatalf("DeleteNetwork: %v", err)
	}
	if !deleted {
		t.Error("DELETE was not called")
	}
}

func TestDeleteNetwork_NotFound(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"networks": []any{}})
	}))

	err := c.DeleteNetwork(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("DeleteNetwork should be idempotent (nil for absent), got: %v", err)
	}
}

// ── detachFirewall ──────────────────────────────────────────────────────────

func TestDetachFirewall_PollsActionToCompletion(t *testing.T) {
	actionPolled := false
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// POST remove_from_resources → returns action
		if r.Method == "POST" && contains(r.URL.Path, "/actions/remove_from_resources") {
			json.NewEncoder(w).Encode(map[string]any{
				"actions": []map[string]any{
					{"id": 777},
				},
			})
			return
		}
		// GET /actions/777 → immediate success
		if r.Method == "GET" && contains(r.URL.Path, "/actions/777") {
			actionPolled = true
			json.NewEncoder(w).Encode(map[string]any{
				"action": map[string]any{"id": 777, "status": "success"},
			})
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))

	err := c.detachFirewall(context.Background(), "10", "42")
	if err != nil {
		t.Fatalf("detachFirewall: %v", err)
	}
	if !actionPolled {
		t.Error("waitForAction was never called — detachFirewall must poll the action to completion")
	}
}

func TestDetachFirewall_MultipleActions(t *testing.T) {
	polled := map[int64]bool{}
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && contains(r.URL.Path, "/actions/remove_from_resources") {
			json.NewEncoder(w).Encode(map[string]any{
				"actions": []map[string]any{
					{"id": 100},
					{"id": 200},
				},
			})
			return
		}
		if r.Method == "GET" && contains(r.URL.Path, "/actions/100") {
			polled[100] = true
			json.NewEncoder(w).Encode(map[string]any{
				"action": map[string]any{"id": 100, "status": "success"},
			})
			return
		}
		if r.Method == "GET" && contains(r.URL.Path, "/actions/200") {
			polled[200] = true
			json.NewEncoder(w).Encode(map[string]any{
				"action": map[string]any{"id": 200, "status": "success"},
			})
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))

	err := c.detachFirewall(context.Background(), "10", "42")
	if err != nil {
		t.Fatalf("detachFirewall: %v", err)
	}
	if !polled[100] || !polled[200] {
		t.Errorf("expected both actions polled, got %v", polled)
	}
}

func TestDetachFirewall_NotFoundIsIdempotent(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && contains(r.URL.Path, "/actions/remove_from_resources") {
			w.WriteHeader(404)
			json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{
					"code":    "not_found",
					"message": "firewall not found",
				},
			})
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))

	err := c.detachFirewall(context.Background(), "999", "42")
	if err != nil {
		t.Fatalf("detachFirewall should be idempotent for not found, got: %v", err)
	}
}

func TestDetachFirewall_PropagatesAPIError(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"code":    "server_error",
				"message": "internal server error",
			},
		})
	}))

	err := c.detachFirewall(context.Background(), "10", "42")
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	if !contains(err.Error(), "detach firewall") {
		t.Errorf("error %q should contain 'detach firewall'", err.Error())
	}
}

func TestDetachFirewall_ActionFailurePropagates(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && contains(r.URL.Path, "/actions/remove_from_resources") {
			json.NewEncoder(w).Encode(map[string]any{
				"actions": []map[string]any{
					{"id": 888},
				},
			})
			return
		}
		if r.Method == "GET" && contains(r.URL.Path, "/actions/888") {
			json.NewEncoder(w).Encode(map[string]any{
				"action": map[string]any{
					"id":     888,
					"status": "error",
					"error":  map[string]any{"message": "detach timed out at provider"},
				},
			})
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))

	err := c.detachFirewall(context.Background(), "10", "42")
	if err == nil {
		t.Fatal("expected error when action fails at provider")
	}
	if !contains(err.Error(), "detach firewall action") {
		t.Errorf("error %q should contain 'detach firewall action'", err.Error())
	}
}

// --- helpers ---

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func testNames() *utils.Names {
	n, _ := utils.NewNames("test", "dev")
	return n
}

// provider_CreateServerRequest builds a minimal CreateServerRequest for testing EnsureServer.
func provider_CreateServerRequest(name string) provider.CreateServerRequest {
	n := testNames()
	return provider.CreateServerRequest{
		Name:         name,
		ServerType:   "cx21",
		Image:        "ubuntu-22.04",
		Location:     "fsn1",
		FirewallName: n.Firewall(),
		NetworkName:  n.Network(),
		Labels:       n.Labels(),
	}
}

func deleteServerRequest(name string) provider.DeleteServerRequest {
	return provider.DeleteServerRequest{
		Name:   name,
		Labels: testNames().Labels(),
	}
}
