package infra

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/getnvoi/nvoi/pkg/testutil"
)

var errTest = errors.New("test error")

func TestAcmeHasCert_MainDomain(t *testing.T) {
	data := []byte(`{"letsencrypt":{"Certificates":[{"domain":{"main":"example.com"},"certificate":"base64data"}]}}`)
	if !acmeHasCert(data, "example.com") {
		t.Error("should find cert for main domain")
	}
}

func TestAcmeHasCert_SAN(t *testing.T) {
	data := []byte(`{"letsencrypt":{"Certificates":[{"domain":{"main":"example.com","sans":["www.example.com"]},"certificate":"base64data"}]}}`)
	if !acmeHasCert(data, "www.example.com") {
		t.Error("should find cert via SAN")
	}
}

func TestAcmeHasCert_NotFound(t *testing.T) {
	data := []byte(`{"letsencrypt":{"Certificates":[{"domain":{"main":"other.com"},"certificate":"base64data"}]}}`)
	if acmeHasCert(data, "example.com") {
		t.Error("should not find cert for different domain")
	}
}

func TestAcmeHasCert_EmptyCertificate(t *testing.T) {
	// Domain exists but no cert data — failed ACME attempt
	data := []byte(`{"letsencrypt":{"Certificates":[{"domain":{"main":"example.com"},"certificate":""}]}}`)
	if acmeHasCert(data, "example.com") {
		t.Error("should not match domain with empty certificate data")
	}
}

func TestAcmeHasCert_DomainInError(t *testing.T) {
	// Domain appears as a string but not in a valid cert entry
	data := []byte(`{"letsencrypt":{"Certificates":[],"Account":{"Email":"admin@example.com"}}}`)
	if acmeHasCert(data, "example.com") {
		t.Error("should not match domain in non-cert fields")
	}
}

func TestAcmeHasCert_InvalidJSON(t *testing.T) {
	if acmeHasCert([]byte("not json"), "example.com") {
		t.Error("should return false for invalid JSON")
	}
}

func TestWaitForCertificate_UsesKubeconfig(t *testing.T) {
	acmeJSON := `{"letsencrypt":{"Certificates":[{"domain":{"main":"example.com"},"certificate":"base64data"}]}}`
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "get deploy traefik", Result: testutil.MockResult{Output: []byte("traefik   1/1   1")}},
			{Prefix: "kubectl -n kube-system exec", Result: testutil.MockResult{Output: []byte(acmeJSON)}},
		},
	}

	err := WaitForCertificate(context.Background(), mock.Run, "example.com")
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	for _, cmd := range mock.Calls {
		if !strings.Contains(cmd, "KUBECONFIG=") {
			t.Errorf("all kubectl calls should set KUBECONFIG, got: %s", cmd)
		}
	}
}

func TestWaitForCertificate_TraefikNotFound_FailsFast(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			// get deploy traefik fails
			{Prefix: "get deploy traefik", Result: testutil.MockResult{Err: errTest}},
			// list deploys for diagnostics
			{Prefix: "get deploy -o name", Result: testutil.MockResult{Output: []byte("deployment.apps/coredns\n")}},
		},
	}

	err := WaitForCertificate(context.Background(), mock.Run, "example.com")
	if err == nil {
		t.Fatal("expected error when traefik deployment is missing")
	}
	if !strings.Contains(err.Error(), "traefik deployment not found") {
		t.Errorf("error should mention traefik not found, got: %s", err)
	}
	if !strings.Contains(err.Error(), "coredns") {
		t.Errorf("error should list available deployments, got: %s", err)
	}
	// Must NOT have entered the 10-minute poll loop
	for _, cmd := range mock.Calls {
		if strings.Contains(cmd, "exec deploy/traefik") {
			t.Error("should not attempt exec when deployment is missing — would waste 10 minutes polling")
		}
	}
}

func TestWaitForCertificate_TraefikNotFound_NoDeploys(t *testing.T) {
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "get deploy traefik", Result: testutil.MockResult{Err: errTest}},
			{Prefix: "get deploy -o name", Result: testutil.MockResult{Output: []byte("")}},
		},
	}

	err := WaitForCertificate(context.Background(), mock.Run, "example.com")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "(none)") {
		t.Errorf("error should show (none) when no deployments exist, got: %s", err)
	}
}

func TestWaitForCertificate_CertFound(t *testing.T) {
	acmeJSON := `{"letsencrypt":{"Certificates":[{"domain":{"main":"example.com"},"certificate":"base64data"}]}}`
	mock := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "get deploy traefik", Result: testutil.MockResult{Output: []byte("traefik   1/1   1")}},
			{Prefix: "exec deploy/traefik", Result: testutil.MockResult{Output: []byte(acmeJSON)}},
		},
	}

	err := WaitForCertificate(context.Background(), mock.Run, "example.com")
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
}

func TestEnsureDocker_AlreadyInstalled(t *testing.T) {
	mock := testutil.NewMockSSH(map[string]testutil.MockResult{
		"sudo docker info >/dev/null 2>&1": {},
	})

	err := EnsureDocker(context.Background(), mock)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
}
