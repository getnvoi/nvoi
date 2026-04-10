package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/getnvoi/nvoi/pkg/utils"
)

func testBucketClient(t *testing.T, handler http.Handler) *Client {
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	c := New(map[string]string{"api_key": "test-key", "account_id": "acct123"})
	c.api.BaseURL = ts.URL
	c.api.HTTPClient = ts.Client()
	return c
}

func TestEnsureBucket(t *testing.T) {
	c := testBucketClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/accounts/acct123/r2/buckets" {
			t.Errorf("path = %q, want %q", r.URL.Path, "/accounts/acct123/r2/buckets")
		}
		// Verify auth header
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-key" {
			t.Errorf("Authorization = %q, want %q", auth, "Bearer test-key")
		}
		// Decode body to verify bucket name
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["name"] != "my-bucket" {
			t.Errorf("body name = %q, want %q", body["name"], "my-bucket")
		}
		w.WriteHeader(200)
	}))

	if err := c.EnsureBucket(context.Background(), "my-bucket"); err != nil {
		t.Fatalf("EnsureBucket: %v", err)
	}
}

func TestEnsureBucket_AlreadyExists(t *testing.T) {
	c := testBucketClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 409 Conflict = bucket already exists
		w.WriteHeader(409)
		w.Write([]byte(`{"errors":[{"code":10006,"message":"bucket already exists"}]}`))
	}))

	if err := c.EnsureBucket(context.Background(), "existing-bucket"); err != nil {
		t.Fatalf("EnsureBucket should succeed for 409 (already exists), got: %v", err)
	}
}

func TestDeleteBucket(t *testing.T) {
	c := testBucketClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		if r.URL.Path != "/accounts/acct123/r2/buckets/my-bucket" {
			t.Errorf("path = %q, want %q", r.URL.Path, "/accounts/acct123/r2/buckets/my-bucket")
		}
		w.WriteHeader(200)
	}))

	if err := c.DeleteBucket(context.Background(), "my-bucket"); err != nil {
		t.Fatalf("DeleteBucket: %v", err)
	}
}

func TestDeleteBucket_AlreadyGone(t *testing.T) {
	c := testBucketClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 404 = bucket already deleted
		w.WriteHeader(404)
		w.Write([]byte(`{"errors":[{"code":10007,"message":"bucket not found"}]}`))
	}))

	if err := c.DeleteBucket(context.Background(), "gone-bucket"); !errors.Is(err, utils.ErrNotFound) {
		t.Fatalf("DeleteBucket should return ErrNotFound for 404 (already gone), got: %v", err)
	}
}

func TestValidateCredentials(t *testing.T) {
	c := testBucketClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/user/tokens/verify" {
			t.Errorf("path = %q, want %q", r.URL.Path, "/user/tokens/verify")
		}
		json.NewEncoder(w).Encode(map[string]any{
			"result": map[string]string{"id": "tok123"},
		})
	}))

	if err := c.ValidateCredentials(context.Background()); err != nil {
		t.Fatalf("ValidateCredentials: %v", err)
	}
}

func TestValidateCredentials_EmptyKey(t *testing.T) {
	c := New(map[string]string{"api_key": "", "account_id": "acct123"})
	err := c.ValidateCredentials(context.Background())
	if err == nil {
		t.Fatal("expected error for empty api_key")
	}
	if !stringContains(err.Error(), "api_key") {
		t.Errorf("error %q should mention api_key", err.Error())
	}
}

func TestValidateCredentials_EmptyAccountID(t *testing.T) {
	c := New(map[string]string{"api_key": "test-key", "account_id": ""})
	err := c.ValidateCredentials(context.Background())
	if err == nil {
		t.Fatal("expected error for empty account_id")
	}
	if !stringContains(err.Error(), "account_id") {
		t.Errorf("error %q should mention account_id", err.Error())
	}
}

func TestCredentials(t *testing.T) {
	c := testBucketClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"result": map[string]string{"id": "tok-abc"},
		})
	}))

	creds, err := c.Credentials(context.Background())
	if err != nil {
		t.Fatalf("Credentials: %v", err)
	}
	if creds.AccessKeyID != "tok-abc" {
		t.Errorf("AccessKeyID = %q, want %q", creds.AccessKeyID, "tok-abc")
	}
	if creds.Endpoint != "https://acct123.r2.cloudflarestorage.com" {
		t.Errorf("Endpoint = %q, want %q", creds.Endpoint, "https://acct123.r2.cloudflarestorage.com")
	}
	if creds.Region != "auto" {
		t.Errorf("Region = %q, want %q", creds.Region, "auto")
	}
	if creds.SecretAccessKey == "" {
		t.Error("SecretAccessKey should not be empty")
	}
}

// --- helper ---

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
