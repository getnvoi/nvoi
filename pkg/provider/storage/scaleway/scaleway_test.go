package scaleway

import (
	"context"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"testing"
)

func testClient(t *testing.T, handler http.Handler) *Client {
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return &Client{
		accessKey: "test-key",
		secretKey: "test-secret",
		region:    "fr-par",
		endpoint:  ts.URL,
	}
}

func TestEnsureBucket_Created(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		w.WriteHeader(200)
	}))

	err := c.EnsureBucket(context.Background(), "test-bucket")
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
}

func TestEnsureBucket_AlreadyExists(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(409) // Conflict = already exists
	}))

	err := c.EnsureBucket(context.Background(), "test-bucket")
	if err != nil {
		t.Fatalf("409 should be idempotent success, got: %v", err)
	}
}

func TestDeleteBucket_Success(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		w.WriteHeader(204)
	}))

	err := c.DeleteBucket(context.Background(), "test-bucket")
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
}

func TestDeleteBucket_NotFound(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))

	err := c.DeleteBucket(context.Background(), "gone-bucket")
	if err != nil {
		t.Fatalf("404 should be idempotent success, got: %v", err)
	}
}

func TestCredentials(t *testing.T) {
	c := &Client{
		accessKey: "ak",
		secretKey: "sk",
		region:    "nl-ams",
		endpoint:  "https://s3.nl-ams.scw.cloud",
	}

	creds, err := c.Credentials(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds.Endpoint != "https://s3.nl-ams.scw.cloud" {
		t.Errorf("endpoint = %q", creds.Endpoint)
	}
	if creds.AccessKeyID != "ak" {
		t.Errorf("access key = %q", creds.AccessKeyID)
	}
	if creds.Region != "nl-ams" {
		t.Errorf("region = %q", creds.Region)
	}
}

func TestValidateCredentials_Missing(t *testing.T) {
	c := &Client{}
	err := c.ValidateCredentials(context.Background())
	if err == nil {
		t.Fatal("expected error for missing credentials")
	}
}

func TestEmptyBucket(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// ListObjects response with no objects
		w.Header().Set("Content-Type", "application/xml")
		xml.NewEncoder(w).Encode(struct {
			XMLName     xml.Name `xml:"ListBucketResult"`
			IsTruncated bool
		}{IsTruncated: false})
	}))

	err := c.EmptyBucket(context.Background(), "test-bucket")
	if err != nil {
		t.Fatalf("empty bucket on empty: %v", err)
	}
}
