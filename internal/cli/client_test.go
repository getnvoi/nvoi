package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAPIError_StructuredJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(422)
		json.NewEncoder(w).Encode(map[string]string{"error": "validation failed"})
	}))
	defer ts.Close()

	c := &APIClient{base: ts.URL, http: ts.Client()}
	err := c.Do("GET", "/test", nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.Status != 422 {
		t.Fatalf("status = %d, want 422", apiErr.Status)
	}
	if apiErr.Message != "validation failed" {
		t.Fatalf("message = %q, want 'validation failed'", apiErr.Message)
	}
}

func TestAPIError_PlainText(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", 500)
	}))
	defer ts.Close()

	c := &APIClient{base: ts.URL, http: ts.Client()}
	err := c.Do("GET", "/test", nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.Status != 500 {
		t.Fatalf("status = %d, want 500", apiErr.Status)
	}
}

func TestAPIError_404(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
	}))
	defer ts.Close()

	c := &APIClient{base: ts.URL, http: ts.Client()}
	err := c.Do("GET", "/test", nil, nil)
	if !IsNotFound(err) {
		t.Fatalf("expected IsNotFound=true, got err=%v", err)
	}
}

func TestAPIError_NonNotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", 403)
	}))
	defer ts.Close()

	c := &APIClient{base: ts.URL, http: ts.Client()}
	err := c.Do("GET", "/test", nil, nil)
	if IsNotFound(err) {
		t.Fatal("403 should not be IsNotFound")
	}
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDoRaw_APIError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(401)
		json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
	}))
	defer ts.Close()

	c := &APIClient{base: ts.URL, http: ts.Client(), stream: ts.Client()}
	_, err := c.DoRaw("GET", "/test")
	if err == nil {
		t.Fatal("expected error")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.Status != 401 {
		t.Fatalf("status = %d, want 401", apiErr.Status)
	}
	if apiErr.Message != "unauthorized" {
		t.Fatalf("message = %q", apiErr.Message)
	}
}

func TestDoRawWithBody_APIError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "bad request"})
	}))
	defer ts.Close()

	c := &APIClient{base: ts.URL, http: ts.Client(), stream: ts.Client()}
	_, err := c.DoRawWithBody("POST", "/test", map[string]string{"key": "val"})
	if err == nil {
		t.Fatal("expected error")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.Status != 400 {
		t.Fatalf("status = %d, want 400", apiErr.Status)
	}
}
