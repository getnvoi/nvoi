package cloudflare

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func testDNSClient(t *testing.T, handler http.Handler) *DNSClient {
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	c := NewDNS(map[string]string{"api_key": "test-key", "zone_id": "zone123", "zone": "example.com"})
	c.api.BaseURL = ts.URL
	c.api.HTTPClient = ts.Client()
	return c
}

func TestListARecords(t *testing.T) {
	mux := http.NewServeMux()
	// ListARecords queries both A and AAAA records
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

	records, err := c.ListARecords(context.Background())
	if err != nil {
		t.Fatalf("ListARecords: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	if records[0].Domain != "app.example.com" {
		t.Errorf("records[0].Domain = %q, want %q", records[0].Domain, "app.example.com")
	}
	if records[0].IP != "1.2.3.4" {
		t.Errorf("records[0].IP = %q, want %q", records[0].IP, "1.2.3.4")
	}
	if records[0].Type != "A" {
		t.Errorf("records[0].Type = %q, want %q", records[0].Type, "A")
	}
	if records[1].Domain != "api.example.com" {
		t.Errorf("records[1].Domain = %q, want %q", records[1].Domain, "api.example.com")
	}
	if records[1].IP != "5.6.7.8" {
		t.Errorf("records[1].IP = %q, want %q", records[1].IP, "5.6.7.8")
	}
}

func TestEnsureARecord_Creates(t *testing.T) {
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

	if err := c.EnsureARecord(context.Background(), "app.example.com", "9.8.7.6", false); err != nil {
		t.Fatalf("EnsureARecord: %v", err)
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

func TestEnsureARecord_AlreadyCorrect(t *testing.T) {
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

	if err := c.EnsureARecord(context.Background(), "app.example.com", "1.2.3.4", false); err != nil {
		t.Fatalf("EnsureARecord: %v", err)
	}
	if requestCount != 1 {
		t.Errorf("expected 1 request (GET only), got %d", requestCount)
	}
}
