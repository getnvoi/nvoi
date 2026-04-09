package cloudflare

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testOriginCAClient(t *testing.T, handler http.Handler) *OriginCAClient {
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	c := NewOriginCA("test-api-key")
	c.api.BaseURL = ts.URL
	c.api.HTTPClient = ts.Client()
	return c
}

func TestCreateCert_Success(t *testing.T) {
	c := testOriginCAClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/certificates" {
			t.Errorf("path = %q, want /certificates", r.URL.Path)
		}

		// Verify auth header
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-api-key" {
			t.Errorf("auth = %q, want %q", auth, "Bearer test-api-key")
		}

		// Parse request body
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)

		hostnames, ok := body["hostnames"].([]any)
		if !ok || len(hostnames) != 2 {
			t.Errorf("hostnames = %v, want 2 entries", body["hostnames"])
		}

		validity, _ := body["requested_validity"].(float64)
		if validity != 5475 {
			t.Errorf("requested_validity = %v, want 5475", validity)
		}

		reqType, _ := body["request_type"].(string)
		if reqType != "origin-ecc" {
			t.Errorf("request_type = %q, want origin-ecc", reqType)
		}

		// Verify CSR is present and valid PEM
		csr, _ := body["csr"].(string)
		block, _ := pem.Decode([]byte(csr))
		if block == nil || block.Type != "CERTIFICATE REQUEST" {
			t.Error("expected valid PEM CERTIFICATE REQUEST in csr field")
		}

		json.NewEncoder(w).Encode(map[string]any{
			"result": map[string]string{
				"certificate": "-----BEGIN CERTIFICATE-----\nMIIB...\n-----END CERTIFICATE-----",
			},
		})
	}))

	cert, err := c.CreateCert(context.Background(), []string{"example.com", "*.example.com"})
	if err != nil {
		t.Fatalf("CreateCert: %v", err)
	}

	if cert.Certificate == "" {
		t.Error("expected non-empty certificate")
	}

	// Verify private key is valid PEM
	block, _ := pem.Decode([]byte(cert.PrivateKey))
	if block == nil || block.Type != "EC PRIVATE KEY" {
		t.Errorf("expected EC PRIVATE KEY PEM, got type %q", block.Type)
	}
}

func TestCreateCert_APIError(t *testing.T) {
	c := testOriginCAClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]any{
			"errors": []map[string]any{
				{"message": "authentication error"},
			},
		})
	}))

	_, err := c.CreateCert(context.Background(), []string{"example.com"})
	if err == nil {
		t.Fatal("expected error on 403")
	}
}

func TestCreateCert_ReturnsID(t *testing.T) {
	c := testOriginCAClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"result": map[string]any{
				"id":          "cert-abc-123",
				"certificate": "-----BEGIN CERTIFICATE-----\nMIIB...\n-----END CERTIFICATE-----",
			},
		})
	}))

	cert, err := c.CreateCert(context.Background(), []string{"example.com"})
	if err != nil {
		t.Fatalf("CreateCert: %v", err)
	}
	if cert.ID != "cert-abc-123" {
		t.Errorf("ID = %q, want %q", cert.ID, "cert-abc-123")
	}
}

func TestListCerts(t *testing.T) {
	c := testOriginCAClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if !strings.HasPrefix(r.URL.Path, "/certificates") {
			t.Errorf("path = %q, want /certificates", r.URL.Path)
		}
		zoneID := r.URL.Query().Get("zone_id")
		if zoneID != "zone-123" {
			t.Errorf("zone_id = %q, want zone-123", zoneID)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"result": []map[string]any{
				{"id": "cert-1", "hostnames": []string{"a.com"}, "expires_on": "2030-01-01"},
				{"id": "cert-2", "hostnames": []string{"b.com", "c.com"}, "expires_on": "2030-01-01"},
			},
		})
	}))

	certs, err := c.ListCerts(context.Background(), "zone-123")
	if err != nil {
		t.Fatalf("ListCerts: %v", err)
	}
	if len(certs) != 2 {
		t.Fatalf("got %d certs, want 2", len(certs))
	}
	if certs[0].ID != "cert-1" {
		t.Errorf("cert[0].ID = %q, want cert-1", certs[0].ID)
	}
	if len(certs[1].Hostnames) != 2 {
		t.Errorf("cert[1].Hostnames = %v, want 2 entries", certs[1].Hostnames)
	}
}

func TestFindCertByHostnames_Match(t *testing.T) {
	c := testOriginCAClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"result": []map[string]any{
				{"id": "cert-1", "hostnames": []string{"other.com"}},
				{"id": "cert-2", "hostnames": []string{"b.com", "a.com"}},
			},
		})
	}))

	found, err := c.FindCertByHostnames(context.Background(), "zone-123", []string{"a.com", "b.com"})
	if err != nil {
		t.Fatalf("FindCertByHostnames: %v", err)
	}
	if found == nil {
		t.Fatal("expected a match")
	}
	if found.ID != "cert-2" {
		t.Errorf("ID = %q, want cert-2", found.ID)
	}
}

func TestFindCertByHostnames_NoMatch(t *testing.T) {
	c := testOriginCAClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"result": []map[string]any{
				{"id": "cert-1", "hostnames": []string{"other.com"}},
			},
		})
	}))

	found, err := c.FindCertByHostnames(context.Background(), "zone-123", []string{"a.com"})
	if err != nil {
		t.Fatalf("FindCertByHostnames: %v", err)
	}
	if found != nil {
		t.Errorf("expected nil match, got %+v", found)
	}
}

func TestRevokeCert_Success(t *testing.T) {
	c := testOriginCAClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Errorf("method = %s, want DELETE", r.Method)
		}
		if r.URL.Path != "/certificates/cert-abc" {
			t.Errorf("path = %q, want /certificates/cert-abc", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"result": map[string]string{"id": "cert-abc"},
		})
	}))

	if err := c.RevokeCert(context.Background(), "cert-abc"); err != nil {
		t.Fatalf("RevokeCert: %v", err)
	}
}

func TestRevokeCert_NotFound(t *testing.T) {
	c := testOriginCAClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]any{
			"errors": []map[string]any{{"message": "not found"}},
		})
	}))

	if err := c.RevokeCert(context.Background(), "gone"); err != nil {
		t.Fatalf("RevokeCert on 404 should succeed (idempotent), got: %v", err)
	}
}

func TestCreateCert_SingleHostname(t *testing.T) {
	var receivedHostnames []any

	c := testOriginCAClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		receivedHostnames, _ = body["hostnames"].([]any)

		json.NewEncoder(w).Encode(map[string]any{
			"result": map[string]string{
				"certificate": "-----BEGIN CERTIFICATE-----\ntest\n-----END CERTIFICATE-----",
			},
		})
	}))

	cert, err := c.CreateCert(context.Background(), []string{"single.example.com"})
	if err != nil {
		t.Fatalf("CreateCert: %v", err)
	}

	if len(receivedHostnames) != 1 {
		t.Errorf("expected 1 hostname, got %d", len(receivedHostnames))
	}

	if cert.Certificate == "" || cert.PrivateKey == "" {
		t.Error("expected both certificate and private key")
	}
}
