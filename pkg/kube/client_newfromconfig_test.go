package kube

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"k8s.io/client-go/rest"
)

// TestNewFromConfig_EndToEnd proves the non-SSH constructor path is real.
//
// Stand up an httptest TLS server impersonating the bits of the apiserver
// `Discovery.ServerVersion()` touches (`GET /version`), build a *rest.Config
// pointing at it, hand it to NewFromConfig, and call ServerVersion through
// the resulting clientset. If the wiring is wrong — wrong transport, missing
// TLS config, kubernetes.NewForConfig refusing the cfg — the call returns
// non-nil error or wrong payload.
//
// This is the load-bearing test for the spec's promise: providers like
// managed Kubernetes (GKE/EKS), Talos, or Daytona's preview-URL'd k3s can
// hand back a *rest.Config and get a working *kube.Client without ever
// involving SSH.
func TestNewFromConfig_EndToEnd(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/version" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"major":      "1",
			"minor":      "31",
			"gitVersion": "v1.31.4",
			"platform":   "linux/amd64",
		})
	}))
	defer srv.Close()

	cfg := &rest.Config{
		Host: srv.URL,
		TLSClientConfig: rest.TLSClientConfig{
			Insecure: true, // httptest cert is self-signed; we trust our own server
		},
	}
	// Belt: ensure the underlying transport doesn't try to verify the test server's cert.
	cfg.TLSClientConfig.CAData = nil
	cfg.Transport = nil
	srv.TLS.InsecureSkipVerify = true
	_ = tls.VersionTLS12 // keep the tls import honest if linting trims it

	c, err := NewFromConfig(cfg)
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}
	if c.Clientset() == nil {
		t.Fatal("Clientset() is nil — NewFromConfig produced an unusable client")
	}
	if c.RESTConfig() != cfg {
		t.Fatal("RESTConfig() did not propagate the cfg pointer")
	}

	// Real RPC through the clientset — proves the cfg is wired end-to-end.
	// kubernetes.Discovery().ServerVersion() issues GET /version.
	got, err := c.Clientset().Discovery().ServerVersion()
	if err != nil {
		t.Fatalf("ServerVersion via NewFromConfig clientset: %v", err)
	}
	if got.GitVersion != "v1.31.4" {
		t.Errorf("ServerVersion.GitVersion = %q, want %q", got.GitVersion, "v1.31.4")
	}

	// Close() must be a no-op when no cleanup is attached (managed-k8s path).
	if err := c.Close(); err != nil {
		t.Fatalf("Close() with no cleanup: %v", err)
	}
}

// TestNewFromConfig_NilConfig guards the API contract: nil cfg in → typed
// error out, no panic. Cheap, but it locks the contract for callers.
func TestNewFromConfig_NilConfig(t *testing.T) {
	c, err := NewFromConfig(nil)
	if err == nil {
		t.Fatal("expected error for nil rest.Config")
	}
	if c != nil {
		t.Fatal("client should be nil when cfg is nil")
	}
}
