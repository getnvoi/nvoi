package kube

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// genKubeconfig returns a minimal valid kubeconfig YAML carrying the given
// CA bytes (as a PEM blob). clientcmd parses it without validating that the
// bytes form a real certificate chain — we only care that buildRESTConfig
// propagates the exact bytes we provided into rest.Config.TLSClientConfig.CAData.
func genKubeconfig(server string, caPEM []byte) []byte {
	caB64 := base64.StdEncoding.EncodeToString(caPEM)
	return []byte(fmt.Sprintf(`apiVersion: v1
kind: Config
current-context: default
clusters:
  - name: local
    cluster:
      server: %s
      certificate-authority-data: %s
contexts:
  - name: default
    context:
      cluster: local
      user: deploy
users:
  - name: deploy
    user:
      token: test-token
`, server, caB64))
}

// TestBuildRESTConfig_IgnoresLocalKubeconfig is the regression test for the
// TLS bug that killed production deploys on dev machines:
//
//	❌  label node X: tls: failed to verify certificate:
//	    x509: certificate signed by unknown authority
//
// buildRESTConfig used to go through clientcmd's deferred-loading path,
// which silently merges in the operator's local ~/.kube/config. When the
// local file existed (normal on dev machines), its CA was used instead of
// the CA we just SFTP-fetched from the fresh k3s master — cert validation
// failed because the local CA doesn't match the fresh cluster's cert.
//
// Strong assertion: plant a local ~/.kube/config with a DIFFERENT CA, call
// buildRESTConfig with our "fetched" bytes, and prove the resulting
// rest.Config uses the fetched CA — never the local one.
func TestBuildRESTConfig_IgnoresLocalKubeconfig(t *testing.T) {
	fetchedCA := []byte("-----BEGIN CERTIFICATE-----\nFETCHED-CA-FROM-K3S-MASTER\n-----END CERTIFICATE-----\n")
	localCA := []byte("-----BEGIN CERTIFICATE-----\nLOCAL-DEV-MACHINE-CA\n-----END CERTIFICATE-----\n")

	// Plant a ~/.kube/config that would be picked up by deferred loading.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KUBECONFIG", "") // defensive — rules out env override
	if err := os.MkdirAll(filepath.Join(home, ".kube"), 0o700); err != nil {
		t.Fatal(err)
	}
	localKC := genKubeconfig("https://bogus.local:6443", localCA)
	if err := os.WriteFile(filepath.Join(home, ".kube", "config"), localKC, 0o600); err != nil {
		t.Fatal(err)
	}

	// "Fetched" bytes — what nvoi SFTP'd from the master.
	fetched := genKubeconfig("https://10.0.1.1:6443", fetchedCA)

	cfg, err := buildRESTConfig(fetched, "127.0.0.1:54321", "10.0.1.1:6443")
	if err != nil {
		t.Fatalf("buildRESTConfig: %v", err)
	}

	got := string(cfg.TLSClientConfig.CAData)

	if !strings.Contains(got, "FETCHED-CA-FROM-K3S-MASTER") {
		t.Errorf("CAData should carry the fetched CA, got:\n%s", got)
	}
	if strings.Contains(got, "LOCAL-DEV-MACHINE-CA") {
		t.Fatalf("REGRESSION — local ~/.kube/config CA leaked through:\n%s", got)
	}
}

func TestBuildRESTConfig_NoLocalKubeconfig(t *testing.T) {
	// Isolated home without any ~/.kube/config — the happy path that worked
	// on a fresh machine even with the bug.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KUBECONFIG", "")

	fetchedCA := []byte("-----BEGIN CERTIFICATE-----\nFRESH-CA\n-----END CERTIFICATE-----\n")
	fetched := genKubeconfig("https://10.0.1.1:6443", fetchedCA)

	cfg, err := buildRESTConfig(fetched, "127.0.0.1:44444", "10.0.1.1:6443")
	if err != nil {
		t.Fatalf("buildRESTConfig: %v", err)
	}
	if !strings.Contains(string(cfg.TLSClientConfig.CAData), "FRESH-CA") {
		t.Errorf("CAData lost: %s", cfg.TLSClientConfig.CAData)
	}
}

func TestBuildRESTConfig_TunnelOverridesServer(t *testing.T) {
	// The raw kubeconfig says server=https://10.0.1.1:6443, but we're
	// dialing through an SSH tunnel on 127.0.0.1:<port>. Both pieces must
	// end up in the rest.Config:
	//   - Host = tunnel address (so dials go to the tunnel)
	//   - ServerName (TLS SNI / cert SAN check) = the real apiserver host,
	//     so the cert presented by the apiserver over the tunnel validates.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KUBECONFIG", "")

	fetched := genKubeconfig("https://10.0.1.1:6443",
		[]byte("-----BEGIN CERTIFICATE-----\nCA\n-----END CERTIFICATE-----\n"))

	cfg, err := buildRESTConfig(fetched, "127.0.0.1:55555", "10.0.1.1:6443")
	if err != nil {
		t.Fatalf("buildRESTConfig: %v", err)
	}
	if cfg.Host != "https://127.0.0.1:55555" {
		t.Errorf("Host = %q, want tunnel address", cfg.Host)
	}
	if cfg.TLSClientConfig.ServerName != "10.0.1.1" {
		t.Errorf("ServerName = %q, want apiserver host (hostOnly)", cfg.TLSClientConfig.ServerName)
	}
}

func TestBuildRESTConfig_MalformedKubeconfig_Errors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KUBECONFIG", "")

	_, err := buildRESTConfig([]byte("this is not yaml ::: !!"), "127.0.0.1:1", "host:6443")
	if err == nil {
		t.Fatal("expected error on malformed kubeconfig")
	}
	if !strings.Contains(err.Error(), "parse kubeconfig") {
		t.Errorf("error = %q, want mention of parse kubeconfig", err.Error())
	}
}
