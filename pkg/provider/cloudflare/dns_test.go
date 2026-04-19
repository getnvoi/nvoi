package cloudflare

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/getnvoi/nvoi/pkg/provider"
)

func testDNSClient(t *testing.T, handler http.Handler) *DNSClient {
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	c := NewDNS(map[string]string{"api_key": "test-key", "zone_id": "zone123", "zone": "example.com"})
	c.api.BaseURL = ts.URL
	c.api.HTTPClient = ts.Client()
	return c
}

func TestListBindings(t *testing.T) {
	mux := http.NewServeMux()
	// ListBindings queries both A and AAAA records
	mux.HandleFunc("/zones/zone123/dns_records", func(w http.ResponseWriter, r *http.Request) {
		rtype := r.URL.Query().Get("type")
		switch rtype {
		case "A":
			json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{"id": "rec1", "type": "A", "name": "app.example.com", "content": "1.2.3.4", "ttl": 300},
					{"id": "rec2", "type": "A", "name": "api.example.com", "content": "5.6.7.8", "ttl": 300},
				},
			})
		case "AAAA":
			json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{},
			})
		default:
			t.Errorf("unexpected record type: %s", rtype)
			w.WriteHeader(400)
		}
	})

	c := testDNSClient(t, mux)

	bindings, err := c.ListBindings(context.Background())
	if err != nil {
		t.Fatalf("ListBindings: %v", err)
	}
	if len(bindings) != 2 {
		t.Fatalf("expected 2 bindings, got %d", len(bindings))
	}
	if bindings[0].Domain != "app.example.com" {
		t.Errorf("bindings[0].Domain = %q, want %q", bindings[0].Domain, "app.example.com")
	}
	if bindings[0].Target != "1.2.3.4" {
		t.Errorf("bindings[0].Target = %q, want %q", bindings[0].Target, "1.2.3.4")
	}
	if bindings[0].Type != "A" {
		t.Errorf("bindings[0].Type = %q, want %q", bindings[0].Type, "A")
	}
	if bindings[1].Domain != "api.example.com" {
		t.Errorf("bindings[1].Domain = %q, want %q", bindings[1].Domain, "api.example.com")
	}
	if bindings[1].Target != "5.6.7.8" {
		t.Errorf("bindings[1].Target = %q, want %q", bindings[1].Target, "5.6.7.8")
	}
}

func TestRouteTo_Creates(t *testing.T) {
	var createdRecord cfDNSRecord
	mux := http.NewServeMux()
	mux.HandleFunc("/zones/zone123/dns_records", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			// listRecords returns empty — no existing record
			json.NewEncoder(w).Encode(map[string]any{"result": []any{}})
		case "POST":
			// createRecord
			json.NewDecoder(r.Body).Decode(&createdRecord)
			w.WriteHeader(200)
			json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{}})
		default:
			t.Errorf("unexpected method: %s", r.Method)
			w.WriteHeader(405)
		}
	})

	c := testDNSClient(t, mux)

	binding := provider.IngressBinding{DNSType: "A", DNSTarget: "9.8.7.6"}
	if err := c.RouteTo(context.Background(), "app.example.com", binding); err != nil {
		t.Fatalf("RouteTo: %v", err)
	}
	if createdRecord.Content != "9.8.7.6" {
		t.Errorf("created record content = %q, want %q", createdRecord.Content, "9.8.7.6")
	}
	if createdRecord.Type != "A" {
		t.Errorf("created record type = %q, want %q", createdRecord.Type, "A")
	}
	if createdRecord.Name != "app.example.com" {
		t.Errorf("created record name = %q, want %q", createdRecord.Name, "app.example.com")
	}
}

func TestRouteTo_AlreadyCorrect(t *testing.T) {
	requestCount := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/zones/zone123/dns_records", func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if r.Method == "GET" {
			// Return existing record with matching IP
			json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{"id": "rec1", "type": "A", "name": "app.example.com", "content": "1.2.3.4", "ttl": 300},
				},
			})
			return
		}
		// No POST or PUT should be made when record is already correct
		t.Errorf("unexpected %s request — record is already correct, no update needed", r.Method)
		w.WriteHeader(500)
	})

	c := testDNSClient(t, mux)

	binding := provider.IngressBinding{DNSType: "A", DNSTarget: "1.2.3.4"}
	if err := c.RouteTo(context.Background(), "app.example.com", binding); err != nil {
		t.Fatalf("RouteTo: %v", err)
	}
	if requestCount != 1 {
		t.Errorf("expected 1 request (GET only), got %d", requestCount)
	}
}

// TestRouteTo_CNAME_ReplacesExistingA verifies that switching from Caddy
// (A record) to tunnel (CNAME) deletes the conflicting A record first.
func TestRouteTo_CNAME_ReplacesExistingA(t *testing.T) {
	var deletedID string
	var createdRecord cfDNSRecord
	mux := http.NewServeMux()
	mux.HandleFunc("/zones/zone123/dns_records", func(w http.ResponseWriter, r *http.Request) {
		rtype := r.URL.Query().Get("type")
		switch r.Method {
		case "GET":
			switch rtype {
			case "CNAME":
				json.NewEncoder(w).Encode(map[string]any{"result": []any{}})
			case "A":
				json.NewEncoder(w).Encode(map[string]any{
					"result": []map[string]any{
						{"id": "a-rec-1", "type": "A", "name": "api.example.com", "content": "1.2.3.4"},
					},
				})
			case "AAAA":
				json.NewEncoder(w).Encode(map[string]any{"result": []any{}})
			}
		case "POST":
			json.NewDecoder(r.Body).Decode(&createdRecord)
			json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{}})
		default:
			t.Errorf("unexpected method %s on collection", r.Method)
			w.WriteHeader(405)
		}
	})
	mux.HandleFunc("/zones/zone123/dns_records/a-rec-1", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		deletedID = "a-rec-1"
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{}})
	})

	c := testDNSClient(t, mux)
	if err := c.RouteTo(context.Background(), "api.example.com",
		provider.IngressBinding{DNSType: "CNAME", DNSTarget: "abc.cfargotunnel.com"}); err != nil {
		t.Fatalf("RouteTo: %v", err)
	}
	if deletedID != "a-rec-1" {
		t.Error("conflicting A record was not deleted before CNAME creation")
	}
	if createdRecord.Type != "CNAME" {
		t.Errorf("created type = %q, want CNAME", createdRecord.Type)
	}
	if createdRecord.Content != "abc.cfargotunnel.com" {
		t.Errorf("created content = %q", createdRecord.Content)
	}
}

// TestRouteTo_A_ReplacesExistingCNAME verifies that switching back from
// tunnel (CNAME) to Caddy (A record) deletes the conflicting CNAME first.
func TestRouteTo_A_ReplacesExistingCNAME(t *testing.T) {
	var deletedID string
	var createdRecord cfDNSRecord
	mux := http.NewServeMux()
	mux.HandleFunc("/zones/zone123/dns_records", func(w http.ResponseWriter, r *http.Request) {
		rtype := r.URL.Query().Get("type")
		switch r.Method {
		case "GET":
			switch rtype {
			case "A":
				json.NewEncoder(w).Encode(map[string]any{"result": []any{}})
			case "CNAME":
				json.NewEncoder(w).Encode(map[string]any{
					"result": []map[string]any{
						{"id": "cname-rec-1", "type": "CNAME", "name": "api.example.com", "content": "abc.cfargotunnel.com"},
					},
				})
			}
		case "POST":
			json.NewDecoder(r.Body).Decode(&createdRecord)
			json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{}})
		default:
			t.Errorf("unexpected method %s", r.Method)
			w.WriteHeader(405)
		}
	})
	mux.HandleFunc("/zones/zone123/dns_records/cname-rec-1", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		deletedID = "cname-rec-1"
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{}})
	})

	c := testDNSClient(t, mux)
	if err := c.RouteTo(context.Background(), "api.example.com",
		provider.IngressBinding{DNSType: "A", DNSTarget: "1.2.3.4"}); err != nil {
		t.Fatalf("RouteTo: %v", err)
	}
	if deletedID != "cname-rec-1" {
		t.Error("conflicting CNAME record was not deleted before A record creation")
	}
	if createdRecord.Type != "A" {
		t.Errorf("created type = %q, want A", createdRecord.Type)
	}
	if createdRecord.Content != "1.2.3.4" {
		t.Errorf("created content = %q", createdRecord.Content)
	}
}

// TestRouteTo_CNAME verifies that CNAME bindings are now supported (#49).
// The Cloudflare DNS provider must create a CNAME record when DNSType == "CNAME".
func TestRouteTo_CNAME(t *testing.T) {
	var createdRecord cfDNSRecord
	mux := http.NewServeMux()
	// List returns empty — no existing CNAME.
	mux.HandleFunc("/zones/zone123/dns_records", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			json.NewEncoder(w).Encode(map[string]any{"result": []any{}})
		case "POST":
			json.NewDecoder(r.Body).Decode(&createdRecord)
			w.WriteHeader(200)
			json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{}})
		default:
			t.Errorf("unexpected method: %s", r.Method)
			w.WriteHeader(405)
		}
	})

	c := testDNSClient(t, mux)
	binding := provider.IngressBinding{DNSType: "CNAME", DNSTarget: "abc123.cfargotunnel.com"}
	if err := c.RouteTo(context.Background(), "api.example.com", binding); err != nil {
		t.Fatalf("RouteTo CNAME: %v", err)
	}
	if createdRecord.Type != "CNAME" {
		t.Errorf("created record type = %q, want %q", createdRecord.Type, "CNAME")
	}
	if createdRecord.Content != "abc123.cfargotunnel.com" {
		t.Errorf("created record content = %q, want %q", createdRecord.Content, "abc123.cfargotunnel.com")
	}
}

// TestRouteTo_CNAME_Proxied verifies that Proxied:true is forwarded to the
// DNS record. Tunnel CNAMEs MUST be proxied — cfargotunnel.com has no
// public IPs unless orange-clouded, causing ERR_CONNECTION_REFUSED.
func TestRouteTo_CNAME_Proxied(t *testing.T) {
	var createdRecord cfDNSRecord
	mux := http.NewServeMux()
	mux.HandleFunc("/zones/zone123/dns_records", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			json.NewEncoder(w).Encode(map[string]any{"result": []any{}})
		case "POST":
			json.NewDecoder(r.Body).Decode(&createdRecord)
			json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{}})
		default:
			t.Errorf("unexpected method: %s", r.Method)
			w.WriteHeader(405)
		}
	})

	c := testDNSClient(t, mux)
	binding := provider.IngressBinding{DNSType: "CNAME", DNSTarget: "a1b2c3.cfargotunnel.com", Proxied: true}
	if err := c.RouteTo(context.Background(), "api.example.com", binding); err != nil {
		t.Fatalf("RouteTo proxied CNAME: %v", err)
	}
	if createdRecord.Type != "CNAME" {
		t.Errorf("created type = %q, want CNAME", createdRecord.Type)
	}
	if createdRecord.Content != "a1b2c3.cfargotunnel.com" {
		t.Errorf("created content = %q", createdRecord.Content)
	}
	if !createdRecord.Proxied {
		t.Error("created record proxied = false, want true — tunnel CNAMEs must be orange-clouded")
	}
}

// TestRouteTo_CNAME_Proxied_UpdatesExisting verifies that a non-proxied CNAME
// is updated to proxied when the binding requests proxied=true.
func TestRouteTo_CNAME_Proxied_UpdatesExisting(t *testing.T) {
	var updatedRecord cfDNSRecord
	mux := http.NewServeMux()
	mux.HandleFunc("/zones/zone123/dns_records", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			// Return existing non-proxied CNAME with same target.
			json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{"id": "cname-1", "type": "CNAME", "name": "api.example.com",
						"content": "a1b2c3.cfargotunnel.com", "proxied": false},
				},
			})
			return
		}
		t.Errorf("unexpected %s on collection", r.Method)
		w.WriteHeader(405)
	})
	mux.HandleFunc("/zones/zone123/dns_records/cname-1", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		json.NewDecoder(r.Body).Decode(&updatedRecord)
		json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{}})
	})

	c := testDNSClient(t, mux)
	binding := provider.IngressBinding{DNSType: "CNAME", DNSTarget: "a1b2c3.cfargotunnel.com", Proxied: true}
	if err := c.RouteTo(context.Background(), "api.example.com", binding); err != nil {
		t.Fatalf("RouteTo proxied CNAME update: %v", err)
	}
	if !updatedRecord.Proxied {
		t.Error("updated record proxied = false, want true — should flip non-proxied to proxied")
	}
}
