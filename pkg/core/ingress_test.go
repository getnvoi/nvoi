package core

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/nvoi/internal/testutil"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// ── Helpers ─────────────────────────────────────────────────────────────────

func ingressCluster(out *testutil.MockOutput, kc *kube.Client, ssh utils.SSHClient, mock *testutil.MockCompute) Cluster {
	provName := fmt.Sprintf("ingress-test-%p", mock)
	provider.RegisterCompute(provName, provider.CredentialSchema{Name: provName}, func(creds map[string]string) provider.ComputeProvider {
		return mock
	})
	return Cluster{
		AppName: "test", Env: "prod",
		Provider:   provName,
		Output:     out,
		MasterKube: kc,
		MasterSSH:  ssh,
		SSHFunc: func(ctx context.Context, addr string) (utils.SSHClient, error) {
			if ssh == nil {
				return nil, fmt.Errorf("no SSH in tests")
			}
			return ssh, nil
		},
	}
}

// ingressKubeWithService returns a kube fake pre-populated with the target
// Service + a ready Traefik deployment so GetServicePort resolves.
func ingressKubeWithService(port int32) *kube.Client {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "nvoi-test-prod"},
		Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: port}}},
	}
	return testKube(svc)
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
	if routes[0].Service != "web" || len(routes[0].Domains) != 2 {
		t.Errorf("route[0] = %+v", routes[0])
	}
	if routes[1].Service != "api" {
		t.Errorf("route[1] service = %q", routes[1].Service)
	}
}

func TestParseIngressArgs_Invalid(t *testing.T) {
	if _, err := ParseIngressArgs([]string{"nodomain"}); err == nil {
		t.Fatal("expected error for missing colon")
	}
	if _, err := ParseIngressArgs([]string{"svc:"}); err == nil {
		t.Fatal("expected error for empty domains")
	}
}

// ── IngressSet ─────────────────────────────────────────────────────────────

func TestIngressSet_ACME_HardErrorWhenUnreachable(t *testing.T) {
	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
	}
	ssh := &testutil.MockSSH{}
	kc := ingressKubeWithService(3000)

	err := IngressSet(context.Background(), IngressSetRequest{
		Cluster: ingressCluster(out, kc, ssh, mock),
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
	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
	}
	kc := ingressKubeWithService(3000)

	err := IngressSet(context.Background(), IngressSetRequest{
		Cluster: ingressCluster(out, kc, &testutil.MockSSH{}, mock),
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
	// The Ingress should have been applied to the fake.
	_, gerr := kc.Clientset().NetworkingV1().Ingresses("nvoi-test-prod").Get(context.Background(), "ingress-web", metav1.GetOptions{})
	if gerr != nil {
		t.Errorf("ingress not applied: %v", gerr)
	}
}

func TestIngressSet_ACME_VerifiesAllDomains(t *testing.T) {
	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
	}
	kc := ingressKubeWithService(3000)
	var certChecked, httpsChecked []string

	err := IngressSet(context.Background(), IngressSetRequest{
		Cluster: ingressCluster(out, kc, &testutil.MockSSH{}, mock),
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
	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
	}
	kc := ingressKubeWithService(3000)

	err := IngressSet(context.Background(), IngressSetRequest{
		Cluster: ingressCluster(out, kc, &testutil.MockSSH{}, mock),
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
	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
	}
	kc := ingressKubeWithService(3000)

	err := IngressSet(context.Background(), IngressSetRequest{
		Cluster: ingressCluster(out, kc, &testutil.MockSSH{}, mock),
		Route:   IngressRouteArg{Service: "web", Domains: []string{"example.com"}},
		ACME:    false,
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
	orig := acmeVerifyTimeout
	acmeVerifyTimeout = 50 * time.Millisecond
	defer func() { acmeVerifyTimeout = orig }()

	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4", PrivateIP: "10.0.1.1"}},
	}
	kc := ingressKubeWithService(3000)

	err := IngressSet(context.Background(), IngressSetRequest{
		Cluster: ingressCluster(out, kc, &testutil.MockSSH{}, mock),
		Route:   IngressRouteArg{Service: "web", Domains: []string{"example.com", "www.example.com"}},
		ACME:    true,
		Hooks: &IngressHooks{
			WaitForCertificate: func(ctx context.Context, ssh utils.SSHClient, domain string) error {
				<-ctx.Done()
				return ctx.Err()
			},
			WaitForHTTPS: func(ctx context.Context, ssh utils.SSHClient, domain, healthPath string) error {
				return nil
			},
		},
	})
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

func TestIngressSet_NoPortErrors(t *testing.T) {
	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master"}},
	}
	// Service has no ports.
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "nvoi-test-prod"},
	}
	kc := testKube(svc)

	err := IngressSet(context.Background(), IngressSetRequest{
		Cluster: ingressCluster(out, kc, &testutil.MockSSH{}, mock),
		Route:   IngressRouteArg{Service: "web", Domains: []string{"example.com"}},
	})
	if err == nil {
		t.Fatal("expected error when service has no port")
	}
	if !strings.Contains(err.Error(), "no port") {
		t.Errorf("error = %q", err.Error())
	}
}

// ── IngressDelete ──────────────────────────────────────────────────────────

func TestIngressDelete_Success(t *testing.T) {
	out := &testutil.MockOutput{}
	mock := &testutil.MockCompute{
		Servers: []*provider.Server{{ID: "1", Name: "nvoi-test-prod-master", IPv4: "1.2.3.4"}},
	}
	kc := testKube(&corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "nvoi-test-prod"},
	})

	err := IngressDelete(context.Background(), IngressDeleteRequest{
		Cluster: ingressCluster(out, kc, &testutil.MockSSH{}, mock),
		Route:   IngressRouteArg{Service: "web", Domains: []string{"example.com"}},
	})
	if err != nil {
		t.Fatalf("expected success: %v", err)
	}
}
