package core

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/getnvoi/nvoi/internal/testutil"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
	"k8s.io/client-go/kubernetes/fake"
)

// ── Helpers ─────────────────────────────────────────────────────────────────

func ingressCluster(out *testutil.MockOutput, ssh utils.SSHClient, mock *testutil.MockCompute) Cluster {
	provName := fmt.Sprintf("ingress-test-%p", mock)
	provider.RegisterCompute(provName, provider.CredentialSchema{Name: provName}, func(creds map[string]string) provider.ComputeProvider {
		return mock
	})
	return Cluster{
		AppName: "test", Env: "prod",
		Provider: provName, Output: out,
		Kube:    kube.NewFromClientset(fake.NewSimpleClientset()),
		SSHFunc: func(ctx context.Context, addr string) (utils.SSHClient, error) { return ssh, nil },
	}
}

func ingressSetSSH() *testutil.MockSSH {
	return &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "create namespace", Result: testutil.MockResult{}},
			{Prefix: "get namespace", Result: testutil.MockResult{}},
			{Prefix: "get service web", Result: testutil.MockResult{Output: []byte("3000")}},
			{Prefix: "replace", Result: testutil.MockResult{}},
			{Prefix: "apply", Result: testutil.MockResult{}},
		},
	}
}

// ── ParseIngressArgs ────────────────────────────────────────────────────────

func TestParseIngressArgs(t *testing.T) {
	routes, err := ParseIngressArgs([]string{
		"web:example.com,www.example.com",
		"api:api.example.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("got %d routes, want 2", len(routes))
	}
	if routes[0].Service != "web" {
		t.Errorf("route[0] service = %q, want web", routes[0].Service)
	}
	if len(routes[0].Domains) != 2 {
		t.Errorf("route[0] domains = %v, want 2", routes[0].Domains)
	}
	if routes[1].Service != "api" {
		t.Errorf("route[1] service = %q, want api", routes[1].Service)
	}
}

func TestParseIngressArgs_Invalid(t *testing.T) {
	_, err := ParseIngressArgs([]string{"nodomain"})
	if err == nil {
		t.Fatal("expected error for missing colon")
	}
	_, err = ParseIngressArgs([]string{"svc:"})
	if err == nil {
		t.Fatal("expected error for empty domains")
	}
}

// ── IngressSet ─────────────────────────────────────────────────────────────

func TestIngressSet_ACME_HardErrorWhenUnreachable(t *testing.T) {
	t.Skip("TODO: needs k8s Service + ACME state in fake clientset")
	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
	}

	err := IngressSet(context.Background(), IngressSetRequest{
		Cluster: ingressCluster(out, ingressSetSSH(), mock),
		Route:   IngressRouteArg{Service: "web", Domains: []string{"example.com"}},
		ACME:    true,
		Hooks: &IngressHooks{
			WaitForCertificate: func(ctx context.Context, ssh utils.SSHClient, domain string) error { return nil },
			WaitForHTTPS: func(ctx context.Context, ssh utils.SSHClient, domain, healthPath string) error {
				return fmt.Errorf("timeout")
			},
		},
	})
	if err == nil {
		t.Fatal("expected hard error when domain unreachable")
	}
	if !strings.Contains(err.Error(), "not reachable") {
		t.Errorf("error should mention 'not reachable', got: %v", err)
	}
}

func TestIngressSet_ACME_Success(t *testing.T) {
	t.Skip("TODO: needs k8s Service + ACME state in fake clientset")
	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
	}
	err := IngressSet(context.Background(), IngressSetRequest{
		Cluster: ingressCluster(out, ingressSetSSH(), mock),
		Route:   IngressRouteArg{Service: "web", Domains: []string{"example.com"}},
		ACME:    true,
		Hooks: &IngressHooks{
			WaitForCertificate: func(ctx context.Context, ssh utils.SSHClient, domain string) error { return nil },
			WaitForHTTPS:       func(ctx context.Context, ssh utils.SSHClient, domain, healthPath string) error { return nil },
		},
	})
	if err != nil {
		t.Fatalf("expected success: %v", err)
	}
}

func TestIngressSet_ACME_VerifiesAllDomains(t *testing.T) {
	t.Skip("TODO: needs k8s Service + ACME state in fake clientset")
	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
	}
	var certChecked, httpsChecked []string
	err := IngressSet(context.Background(), IngressSetRequest{
		Cluster: ingressCluster(out, ingressSetSSH(), mock),
		Route:   IngressRouteArg{Service: "web", Domains: []string{"example.com", "www.example.com"}},
		ACME:    true,
		Hooks: &IngressHooks{
			WaitForCertificate: func(ctx context.Context, ssh utils.SSHClient, domain string) error {
				certChecked = append(certChecked, domain)
				return nil
			},
			WaitForHTTPS: func(ctx context.Context, ssh utils.SSHClient, domain, healthPath string) error {
				httpsChecked = append(httpsChecked, domain)
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("expected success: %v", err)
	}
	if len(certChecked) != 2 {
		t.Errorf("expected cert check for 2 domains, got %d: %v", len(certChecked), certChecked)
	}
	if len(httpsChecked) != 2 {
		t.Errorf("expected HTTPS check for 2 domains, got %d: %v", len(httpsChecked), httpsChecked)
	}
}

func TestIngressSet_ACME_SecondDomainCertFails(t *testing.T) {
	t.Skip("TODO: needs k8s Service + ACME state in fake clientset")
	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
	}
	err := IngressSet(context.Background(), IngressSetRequest{
		Cluster: ingressCluster(out, ingressSetSSH(), mock),
		Route:   IngressRouteArg{Service: "web", Domains: []string{"example.com", "www.example.com"}},
		ACME:    true,
		Hooks: &IngressHooks{
			WaitForCertificate: func(ctx context.Context, ssh utils.SSHClient, domain string) error {
				if domain == "www.example.com" {
					return fmt.Errorf("cert timeout")
				}
				return nil
			},
			WaitForHTTPS: func(ctx context.Context, ssh utils.SSHClient, domain, healthPath string) error {
				return nil
			},
		},
	})
	if err == nil {
		t.Fatal("expected error when second domain cert fails")
	}
	if !strings.Contains(err.Error(), "www.example.com") {
		t.Errorf("error should mention the failing domain, got: %v", err)
	}
}

func TestIngressSet_NoACME_SkipsHTTPSWait(t *testing.T) {
	t.Skip("TODO: needs k8s Service + ACME state in fake clientset")
	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
	}
	err := IngressSet(context.Background(), IngressSetRequest{
		Cluster: ingressCluster(out, ingressSetSSH(), mock),
		Route:   IngressRouteArg{Service: "web", Domains: []string{"example.com"}},
		ACME:    false, // tunnel mode — no HTTPS wait
		Hooks: &IngressHooks{
			WaitForHTTPS: func(ctx context.Context, ssh utils.SSHClient, domain, healthPath string) error {
				t.Fatal("WaitForHTTPS should not be called when ACME is false")
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("expected success: %v", err)
	}
}

func TestIngressSet_ACME_TimeoutWarnsInsteadOfFailing(t *testing.T) {
	t.Skip("TODO: needs k8s Service + ACME state in fake clientset")
	// Shrink the deadline so the test doesn't wait 10 minutes.
	orig := acmeVerifyTimeout
	acmeVerifyTimeout = 50 * time.Millisecond
	defer func() { acmeVerifyTimeout = orig }()

	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
	}
	err := IngressSet(context.Background(), IngressSetRequest{
		Cluster: ingressCluster(out, ingressSetSSH(), mock),
		Route:   IngressRouteArg{Service: "web", Domains: []string{"example.com", "www.example.com"}},
		ACME:    true,
		Hooks: &IngressHooks{
			WaitForCertificate: func(ctx context.Context, ssh utils.SSHClient, domain string) error {
				// Block until context expires.
				<-ctx.Done()
				return ctx.Err()
			},
			WaitForHTTPS: func(ctx context.Context, ssh utils.SSHClient, domain, healthPath string) error {
				return nil
			},
		},
	})
	// Must NOT return an error — timeout is a warning, not a failure.
	if err != nil {
		t.Fatalf("timeout should warn, not fail. got: %v", err)
	}
	if len(out.Warnings) == 0 {
		t.Fatal("expected a warning about ACME verification timeout")
	}
	found := false
	for _, w := range out.Warnings {
		if strings.Contains(w, "timed out") {
			found = true
		}
	}
	if !found {
		t.Errorf("warning should mention 'timed out', got: %v", out.Warnings)
	}
}

func TestIngressSet_NoCluster(t *testing.T) {
	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{Servers: nil}

	err := IngressSet(context.Background(), IngressSetRequest{
		Cluster: ingressCluster(out, nil, mock),
		Route:   IngressRouteArg{Service: "web", Domains: []string{"example.com"}},
	})
	if err == nil {
		t.Fatal("expected error when no cluster exists")
	}
}

// ── IngressDelete ───────────────────────────────────────────────────────────

func TestIngressDelete_Success(t *testing.T) {
	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
	}
	ssh := &testutil.MockSSH{
		Prefixes: []testutil.MockPrefix{
			{Prefix: "get namespace", Result: testutil.MockResult{}},
			{Prefix: "create namespace", Result: testutil.MockResult{}},
			{Prefix: "delete ingress", Result: testutil.MockResult{}},
		},
	}

	err := IngressDelete(context.Background(), IngressDeleteRequest{
		Cluster: ingressCluster(out, ssh, mock),
		Route:   IngressRouteArg{Service: "web", Domains: []string{"example.com"}},
	})
	if err != nil {
		t.Fatalf("expected success: %v", err)
	}
}

func TestIngressDelete_ClusterGone(t *testing.T) {
	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{Servers: nil} // ErrNoMaster

	err := IngressDelete(context.Background(), IngressDeleteRequest{
		Cluster: ingressCluster(out, nil, mock),
		Route:   IngressRouteArg{Service: "web", Domains: []string{"example.com"}},
	})
	if err != nil {
		t.Fatalf("expected success: %v", err)
	}
}
