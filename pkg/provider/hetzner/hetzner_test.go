package hetzner

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

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
		// getServerByName is called with GET /servers?name=existing-server
		if r.Method == "GET" && contains(r.URL.String(), "/servers") {
			json.NewEncoder(w).Encode(map[string]any{
				"servers": []map[string]any{
					{
						"id":     42,
						"name":   "existing-server",
						"status": "running",
						"public_net": map[string]any{
							"ipv4": map[string]string{"ip": "5.6.7.8"},
							"ipv6": map[string]string{"ip": ""},
						},
						"private_net": []map[string]string{},
					},
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
	if !errors.Is(err, utils.ErrNotFound) {
		t.Fatalf("DeleteServer should return ErrNotFound for non-existent server, got: %v", err)
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

// ── Firewall rule reconciliation ────────────────────────────────────────────────

func TestEnsureFirewall_ReconcileExistingRules(t *testing.T) {
	setRulesCalled := false
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// GET /firewalls?name=... returns existing firewall
		if r.Method == "GET" && contains(r.URL.String(), "/firewalls") {
			json.NewEncoder(w).Encode(map[string]any{
				"firewalls": []map[string]any{
					{"id": 99, "name": "nvoi-test-fw"},
				},
			})
			return
		}
		// POST /firewalls/99/actions/set_rules — the reconciliation call
		if r.Method == "POST" && contains(r.URL.Path, "/firewalls/99/actions/set_rules") {
			setRulesCalled = true
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			rules, ok := body["rules"].([]any)
			if !ok || len(rules) != 5 {
				t.Errorf("expected 5 base rules in set_rules, got %v", len(rules))
			}
			w.WriteHeader(200)
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))

	id, err := c.ensureFirewall(context.Background(), "nvoi-test-fw", map[string]string{"app": "test"})
	if err != nil {
		t.Fatalf("ensureFirewall: %v", err)
	}
	if id != "99" {
		t.Errorf("id = %q, want %q", id, "99")
	}
	if !setRulesCalled {
		t.Error("set_rules was NOT called on existing firewall — rules not reconciled")
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
	if err == nil || !errors.Is(err, utils.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got: %v", err)
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
	if err == nil || !errors.Is(err, utils.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got: %v", err)
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
