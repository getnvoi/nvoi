package kube

import (
	"context"
	"sort"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/getnvoi/nvoi/pkg/utils"
)

func TestBuildIngress_SingleDomainACME(t *testing.T) {
	ing := BuildIngress(IngressRoute{Service: "web", Port: 3000, Domains: []string{"example.com"}}, "nvoi-test-prod", true)

	if ing.Name != "ingress-web" {
		t.Errorf("name = %q", ing.Name)
	}
	if ing.Namespace != "nvoi-test-prod" {
		t.Errorf("namespace = %q", ing.Namespace)
	}
	if ing.Annotations["traefik.ingress.kubernetes.io/router.tls.certresolver"] != "letsencrypt" {
		t.Errorf("ACME annotation missing: %v", ing.Annotations)
	}
	if len(ing.Spec.TLS) != 1 || ing.Spec.TLS[0].SecretName != "tls-web" {
		t.Errorf("TLS = %+v", ing.Spec.TLS)
	}
	rules := ing.Spec.Rules
	if len(rules) != 1 || rules[0].Host != "example.com" {
		t.Fatalf("rules = %+v", rules)
	}
	backend := rules[0].HTTP.Paths[0].Backend.Service
	if backend.Name != "web" || backend.Port.Number != 3000 {
		t.Errorf("backend = %+v", backend)
	}
}

func TestBuildIngress_MultipleDomains(t *testing.T) {
	ing := BuildIngress(IngressRoute{
		Service: "api", Port: 8080,
		Domains: []string{"api.example.com", "api2.example.com"},
	}, "ns", true)

	if len(ing.Spec.Rules) != 2 {
		t.Fatalf("rules = %d, want 2", len(ing.Spec.Rules))
	}
	if len(ing.Spec.TLS[0].Hosts) != 2 {
		t.Errorf("TLS hosts = %v", ing.Spec.TLS[0].Hosts)
	}
}

func TestBuildIngress_NoACME(t *testing.T) {
	ing := BuildIngress(IngressRoute{Service: "web", Port: 80, Domains: []string{"example.com"}}, "ns", false)
	if _, has := ing.Annotations["traefik.ingress.kubernetes.io/router.tls.certresolver"]; has {
		t.Error("ACME annotation must not be present when acme=false")
	}
	if len(ing.Spec.TLS) != 0 {
		t.Errorf("TLS must be empty when acme=false, got %+v", ing.Spec.TLS)
	}
}

func TestBuildIngress_Labels(t *testing.T) {
	ing := BuildIngress(IngressRoute{Service: "web", Port: 80, Domains: []string{"example.com"}}, "ns", true)
	if ing.Labels[utils.LabelAppManagedBy] != utils.LabelManagedBy {
		t.Errorf("managed-by label missing: %v", ing.Labels)
	}
}

func TestKubeIngressName(t *testing.T) {
	if got := KubeIngressName("web"); got != "ingress-web" {
		t.Errorf("got %q", got)
	}
}

func TestDeleteIngress_Idempotent(t *testing.T) {
	c := newTestClient()
	if err := c.DeleteIngress(context.Background(), "ns", "missing"); err != nil {
		t.Errorf("absent ingress must not error: %v", err)
	}
	existing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "ingress-web", Namespace: "ns"},
	}
	c = newTestClient(existing)
	if err := c.DeleteIngress(context.Background(), "ns", "web"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := c.cs.NetworkingV1().Ingresses("ns").Get(context.Background(), "ingress-web", metav1.GetOptions{}); err == nil {
		t.Error("ingress should be gone")
	}
}

func managedIngress(name, host, svc string, port int32) *networkingv1.Ingress {
	pathType := networkingv1.PathTypePrefix
	return &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "ns",
			Labels:    map[string]string{utils.LabelAppManagedBy: utils.LabelManagedBy},
		},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{{
				Host: host,
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Path:     "/",
							PathType: &pathType,
							Backend: networkingv1.IngressBackend{
								Service: &networkingv1.IngressServiceBackend{
									Name: svc,
									Port: networkingv1.ServiceBackendPort{Number: port},
								},
							},
						}},
					},
				},
			}},
		},
	}
}

func TestGetIngressRoutes_ParsesServices(t *testing.T) {
	c := newTestClient(
		managedIngress("ingress-web", "example.com", "web", 80),
		managedIngress("ingress-api", "api.example.com", "api", 8080),
	)

	routes, err := c.GetIngressRoutes(context.Background(), "ns")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("routes = %d, want 2", len(routes))
	}
	byService := map[string]IngressRoute{}
	for _, r := range routes {
		byService[r.Service] = r
	}
	if byService["web"].Port != 80 || byService["web"].Domains[0] != "example.com" {
		t.Errorf("web = %+v", byService["web"])
	}
	if byService["api"].Port != 8080 || byService["api"].Domains[0] != "api.example.com" {
		t.Errorf("api = %+v", byService["api"])
	}
}

func TestGetIngressRoutes_SameServiceMerges(t *testing.T) {
	c := newTestClient(
		managedIngress("ingress-web-1", "a.example.com", "web", 80),
		managedIngress("ingress-web-2", "b.example.com", "web", 80),
	)

	routes, err := c.GetIngressRoutes(context.Background(), "ns")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("routes = %d, want 1 (merged)", len(routes))
	}
	domains := routes[0].Domains
	sort.Strings(domains)
	if len(domains) != 2 || domains[0] != "a.example.com" || domains[1] != "b.example.com" {
		t.Errorf("domains = %v", domains)
	}
}

func TestGetIngressRoutes_Empty(t *testing.T) {
	c := newTestClient()
	routes, err := c.GetIngressRoutes(context.Background(), "ns")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(routes) != 0 {
		t.Errorf("routes = %v", routes)
	}
}

func TestGetIngressRoutes_SkipsRulesWithoutHTTP(t *testing.T) {
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "broken",
			Namespace: "ns",
			Labels:    map[string]string{utils.LabelAppManagedBy: utils.LabelManagedBy},
		},
		Spec: networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{Host: "x"}}}, // no HTTP
	}
	c := newTestClient(ing)
	routes, err := c.GetIngressRoutes(context.Background(), "ns")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(routes) != 0 {
		t.Errorf("rules without HTTP must be skipped, got %v", routes)
	}
}

func TestEnsureTraefikACME_AppliesHelmChartConfig(t *testing.T) {
	one := int32(1)
	readyTraefik := &appsv1.Deployment{
		TypeMeta:   metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{Name: "traefik", Namespace: "kube-system"},
		Spec:       appsv1.DeploymentSpec{Replicas: &one},
		Status:     appsv1.DeploymentStatus{ReadyReplicas: 1},
	}
	c := newTestClient(readyTraefik)

	cleanup := fastTiming()
	defer cleanup()

	if err := c.EnsureTraefikACME(context.Background(), "admin@example.com", true); err != nil {
		t.Fatalf("apply: %v", err)
	}
	gvr := schema.GroupVersionResource{Group: "helm.cattle.io", Version: "v1", Resource: "helmchartconfigs"}
	got, err := c.dyn.Resource(gvr).Namespace("kube-system").Get(context.Background(), "traefik", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("HelmChartConfig not applied: %v", err)
	}
	values, _, _ := unstructuredGet(got.Object, "spec", "valuesContent")
	vs, _ := values.(string)
	if !contains(vs, "admin@example.com") {
		t.Errorf("valuesContent missing email: %q", vs)
	}
	if !contains(vs, "letsencrypt") {
		t.Errorf("valuesContent missing letsencrypt: %q", vs)
	}
}

func TestEnsureTraefikACME_NoACMEMode(t *testing.T) {
	one := int32(1)
	readyTraefik := &appsv1.Deployment{
		TypeMeta:   metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{Name: "traefik", Namespace: "kube-system"},
		Spec:       appsv1.DeploymentSpec{Replicas: &one},
		Status:     appsv1.DeploymentStatus{ReadyReplicas: 1},
	}
	c := newTestClient(readyTraefik)

	cleanup := fastTiming()
	defer cleanup()

	if err := c.EnsureTraefikACME(context.Background(), "", false); err != nil {
		t.Fatalf("apply: %v", err)
	}
	gvr := schema.GroupVersionResource{Group: "helm.cattle.io", Version: "v1", Resource: "helmchartconfigs"}
	got, err := c.dyn.Resource(gvr).Namespace("kube-system").Get(context.Background(), "traefik", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("HelmChartConfig not applied: %v", err)
	}
	values, _, _ := unstructuredGet(got.Object, "spec", "valuesContent")
	vs, _ := values.(string)
	if contains(vs, "letsencrypt") {
		t.Errorf("no-ACME mode must not configure letsencrypt, got: %q", vs)
	}
}

// unstructuredGet fetches a nested value from an unstructured.Unstructured
// object's raw map. Exposed to tests via file-scope helper so we don't couple
// the test to a specific k8s helper import path.
func unstructuredGet(obj map[string]any, path ...string) (any, bool, error) {
	cur := any(obj)
	for _, key := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false, nil
		}
		cur, ok = m[key]
		if !ok {
			return nil, false, nil
		}
	}
	return cur, true, nil
}
