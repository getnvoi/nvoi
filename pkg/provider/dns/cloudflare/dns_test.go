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

// TestRouteTo_RejectsCNAME locks the v1 contract: CNAME bindings are
// reserved for the managed-k8s / tunnel-provider work (#48 / #49) and
// must surface a clear, traceable error today rather than silently
// fall through to A-record upsert with a hostname target.
func TestRouteTo_RejectsCNAME(t *testing.T) {
	c := testDNSClient(t, http.NewServeMux())
	binding := provider.IngressBinding{DNSType: "CNAME", DNSTarget: "lb.aws.com"}
	err := c.RouteTo(context.Background(), "api.example.com", binding)
	if err == nil {
		t.Fatal("expected error for CNAME binding")
	}
	if got := err.Error(); !contains(got, "CNAME") || !contains(got, "#48") {
		t.Errorf("error message should mention CNAME and #48, got: %q", got)
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
