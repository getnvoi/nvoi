package reconcile

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/nvoi/internal/config"
	app "github.com/getnvoi/nvoi/pkg/core"
)

// init shrinks the ACME verification timeout for reconcile tests so the ACME
// path warns and returns quickly instead of trying to poll real Traefik state.
// A real deploy still uses the production 10m timeout.
func init() {
	app.SetACMEVerifyTimeoutForTest(time.Millisecond)
}

// seedService pre-populates the fake with a Service that has Port, so
// GetServicePort succeeds inside IngressSet.
func seedService(t *testing.T, dc *config.DeployContext, svcName string, port int32) {
	t.Helper()
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: svcName, Namespace: testNS},
		Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: port}}},
	}
	if _, err := kfFor(dc).Typed.CoreV1().Services(testNS).Create(context.Background(), svc, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed service: %v", err)
	}
}

func TestIngress_FreshDeploy(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	seedService(t, dc, "web", 80)

	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{"web": {Image: "nginx", Port: 80}},
		Domains:  map[string][]string{"web": {"myapp.com"}},
	}

	if err := Ingress(context.Background(), dc, nil, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !kfFor(dc).HasIngress(testNS, "ingress-web") {
		t.Error("ingress-web not applied")
	}
}

func TestIngress_NoDomains(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers: map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
	}

	if err := Ingress(context.Background(), dc, nil, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestIngress_OrphanRemovedWhenDomainsDropped(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	kf := kfFor(dc)

	_, err := kf.Typed.NetworkingV1().Ingresses(testNS).Create(context.Background(),
		&networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: "ingress-web", Namespace: testNS}},
		metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{"web": {Image: "nginx", Port: 80}},
	}
	live := &config.LiveState{
		Domains: map[string][]string{"web": {"myapp.com"}},
	}

	if err := Ingress(context.Background(), dc, live, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if kf.HasIngress(testNS, "ingress-web") {
		t.Error("orphan ingress-web should have been removed")
	}
}

func TestIngress_OrphanRemovedWhenServiceDropped(t *testing.T) {
	ssh := convergeMock()
	dc := testDC(ssh)
	kf := kfFor(dc)

	seedService(t, dc, "web", 80)
	for _, name := range []string{"ingress-web", "ingress-api"} {
		_, err := kf.Typed.NetworkingV1().Ingresses(testNS).Create(context.Background(),
			&networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS}},
			metav1.CreateOptions{})
		if err != nil {
			t.Fatal(err)
		}
	}

	cfg := &config.AppConfig{
		App: "myapp", Env: "prod",
		Servers:  map[string]config.ServerDef{"master": {Type: "cx23", Region: "fsn1", Role: "master"}},
		Services: map[string]config.ServiceDef{"web": {Image: "nginx", Port: 80}},
		Domains:  map[string][]string{"web": {"myapp.com"}},
	}
	live := &config.LiveState{
		Domains: map[string][]string{
			"web": {"myapp.com"},
			"api": {"api.myapp.com"},
		},
	}

	if err := Ingress(context.Background(), dc, live, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if kf.HasIngress(testNS, "ingress-api") {
		t.Error("orphan ingress-api should have been removed")
	}
	if !kf.HasIngress(testNS, "ingress-web") {
		t.Error("desired ingress-web should still exist")
	}
}
