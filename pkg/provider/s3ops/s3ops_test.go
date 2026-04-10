package s3ops

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/pkg/provider"
)

func testS3Creds(t *testing.T, handler http.Handler) (provider.BucketCredentials, func()) {
	t.Helper()
	ts := httptest.NewServer(handler)
	creds := provider.BucketCredentials{
		Endpoint:        ts.URL,
		AccessKeyID:     "test-key",
		SecretAccessKey: "test-secret",
		Region:          "auto",
	}
	return creds, ts.Close
}

func TestS3EmptyBucket_AlreadyEmpty(t *testing.T) {
	creds, close := testS3Creds(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// ListObjectsV2 returns empty
		w.WriteHeader(200)
		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
			<ListBucketResult>
				<IsTruncated>false</IsTruncated>
			</ListBucketResult>`))
	}))
	defer close()

	if err := EmptyBucket(context.Background(), creds, "my-bucket"); err != nil {
		t.Fatalf("EmptyBucket: %v", err)
	}
}

func TestS3EmptyBucket_DeletesObjects(t *testing.T) {
	calls := 0
	creds, close := testS3Creds(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method == "GET" {
			if calls == 1 {
				// First list: return 2 objects
				w.WriteHeader(200)
				w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
					<ListBucketResult>
						<Contents><Key>file1.txt</Key></Contents>
						<Contents><Key>file2.txt</Key></Contents>
						<IsTruncated>false</IsTruncated>
					</ListBucketResult>`))
				return
			}
			// Second list: empty
			w.WriteHeader(200)
			w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
				<ListBucketResult><IsTruncated>false</IsTruncated></ListBucketResult>`))
			return
		}
		if r.Method == "POST" && strings.Contains(r.URL.RawQuery, "delete") {
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "file1.txt") || !strings.Contains(string(body), "file2.txt") {
				t.Errorf("delete body should contain both keys, got: %s", string(body))
			}
			w.WriteHeader(200)
			return
		}
		t.Errorf("unexpected %s %s", r.Method, r.URL)
		w.WriteHeader(500)
	}))
	defer close()

	if err := EmptyBucket(context.Background(), creds, "my-bucket"); err != nil {
		t.Fatalf("EmptyBucket: %v", err)
	}
}

func TestS3EmptyBucket_404_Succeeds(t *testing.T) {
	creds, close := testS3Creds(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer close()

	if err := EmptyBucket(context.Background(), creds, "gone-bucket"); err != nil {
		t.Fatalf("EmptyBucket should succeed on 404 (bucket gone), got: %v", err)
	}
}

func TestS3SetCORS(t *testing.T) {
	var receivedBody string
	creds, close := testS3Creds(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		if !strings.Contains(r.URL.RawQuery, "cors") {
			t.Errorf("query should contain cors, got %s", r.URL.RawQuery)
		}
		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)
		w.WriteHeader(200)
	}))
	defer close()

	err := SetCORS(context.Background(), creds, "my-bucket", []string{"https://example.com"}, []string{"GET", "PUT"})
	if err != nil {
		t.Fatalf("SetCORS: %v", err)
	}
	if !strings.Contains(receivedBody, "https://example.com") {
		t.Errorf("body should contain origin, got: %s", receivedBody)
	}
	if !strings.Contains(receivedBody, "<AllowedMethod>GET</AllowedMethod>") {
		t.Errorf("body should contain GET method, got: %s", receivedBody)
	}
}

func TestS3SetCORS_DefaultMethods(t *testing.T) {
	var receivedBody string
	creds, close := testS3Creds(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)
		w.WriteHeader(200)
	}))
	defer close()

	// nil methods should default to GET, PUT, POST, DELETE
	err := SetCORS(context.Background(), creds, "my-bucket", []string{"*"}, nil)
	if err != nil {
		t.Fatalf("SetCORS: %v", err)
	}
	for _, m := range []string{"GET", "PUT", "POST", "DELETE"} {
		if !strings.Contains(receivedBody, "<AllowedMethod>"+m+"</AllowedMethod>") {
			t.Errorf("body should contain default method %s", m)
		}
	}
}

func TestS3SetCORS_Error(t *testing.T) {
	creds, close := testS3Creds(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte("AccessDenied"))
	}))
	defer close()

	err := SetCORS(context.Background(), creds, "my-bucket", []string{"*"}, nil)
	if err == nil {
		t.Fatal("expected error on 403")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error should contain status code, got: %v", err)
	}
}

func TestS3ClearCORS(t *testing.T) {
	creds, close := testS3Creds(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		if !strings.Contains(r.URL.RawQuery, "cors") {
			t.Errorf("query should contain cors, got %s", r.URL.RawQuery)
		}
		w.WriteHeader(204)
	}))
	defer close()

	if err := ClearCORS(context.Background(), creds, "my-bucket"); err != nil {
		t.Fatalf("ClearCORS: %v", err)
	}
}

func TestS3ClearCORS_404_Succeeds(t *testing.T) {
	creds, close := testS3Creds(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404) // no cors config — fine
	}))
	defer close()

	if err := ClearCORS(context.Background(), creds, "my-bucket"); err != nil {
		t.Fatalf("ClearCORS should succeed on 404 (no cors), got: %v", err)
	}
}

func TestS3SetLifecycle(t *testing.T) {
	var receivedBody string
	creds, close := testS3Creds(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		if !strings.Contains(r.URL.RawQuery, "lifecycle") {
			t.Errorf("query should contain lifecycle, got %s", r.URL.RawQuery)
		}
		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)
		w.WriteHeader(200)
	}))
	defer close()

	err := SetLifecycle(context.Background(), creds, "my-bucket", 30)
	if err != nil {
		t.Fatalf("SetLifecycle: %v", err)
	}
	if !strings.Contains(receivedBody, "<Days>30</Days>") {
		t.Errorf("body should contain expiry days, got: %s", receivedBody)
	}
	if !strings.Contains(receivedBody, "nvoi-expire") {
		t.Errorf("body should contain rule ID, got: %s", receivedBody)
	}
}

func TestS3SetLifecycle_Error(t *testing.T) {
	creds, close := testS3Creds(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("InternalError"))
	}))
	defer close()

	err := SetLifecycle(context.Background(), creds, "my-bucket", 7)
	if err == nil {
		t.Fatal("expected error on 500")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should contain status code, got: %v", err)
	}
}
