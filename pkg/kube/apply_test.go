package kube

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestApply_CreatesNamespace(t *testing.T) {
	c := newTestClient()
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "nvoi-myapp-prod"}}

	if err := c.Apply(context.Background(), "", ns); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := c.cs.CoreV1().Namespaces().Get(context.Background(), "nvoi-myapp-prod", metav1.GetOptions{}); err != nil {
		t.Fatalf("namespace not created: %v", err)
	}
}

func TestApply_UpdatesExistingDeployment(t *testing.T) {
	two := int32(2)
	existing := &appsv1.Deployment{
		TypeMeta:   metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "ns"},
		Spec:       appsv1.DeploymentSpec{Replicas: &two},
	}
	c := newTestClient(existing)

	three := int32(3)
	updated := &appsv1.Deployment{
		TypeMeta:   metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "ns"},
		Spec:       appsv1.DeploymentSpec{Replicas: &three},
	}
	if err := c.Apply(context.Background(), "ns", updated); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got, err := c.cs.AppsV1().Deployments("ns").Get(context.Background(), "web", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if *got.Spec.Replicas != 3 {
		t.Errorf("replicas = %d, want 3", *got.Spec.Replicas)
	}
}

func TestApply_PreservesServiceClusterIP(t *testing.T) {
	existing := &corev1.Service{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Service"},
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "ns", ResourceVersion: "7"},
		Spec:       corev1.ServiceSpec{ClusterIP: "10.0.0.42"},
	}
	c := newTestClient(existing)

	updated := &corev1.Service{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Service"},
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "ns"},
		Spec:       corev1.ServiceSpec{}, // no ClusterIP — would fail on k8s update without preservation
	}
	if err := c.Apply(context.Background(), "ns", updated); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got, err := c.cs.CoreV1().Services("ns").Get(context.Background(), "web", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Spec.ClusterIP != "10.0.0.42" {
		t.Errorf("ClusterIP lost on update: got %q", got.Spec.ClusterIP)
	}
}

func TestApply_MissingName(t *testing.T) {
	c := newTestClient()
	dep := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
	}
	err := c.Apply(context.Background(), "ns", dep)
	if err == nil || !contains(err.Error(), "missing metadata.name") {
		t.Fatalf("expected missing-name error, got: %v", err)
	}
}

func TestApply_UnstructuredCRD(t *testing.T) {
	// Unstructured HelmChartConfig — exercises the dynamic-client path.
	c := newTestClient()
	hcc := &unstructured.Unstructured{}
	hcc.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "helm.cattle.io", Version: "v1", Kind: "HelmChartConfig",
	})
	hcc.SetName("traefik")
	hcc.SetNamespace("kube-system")

	if err := c.Apply(context.Background(), "kube-system", hcc); err != nil {
		t.Fatalf("apply CRD: %v", err)
	}
}

func TestEnsureNamespace_Idempotent(t *testing.T) {
	c := newTestClient()
	ctx := context.Background()
	if err := c.EnsureNamespace(ctx, "nvoi-myapp-prod"); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := c.EnsureNamespace(ctx, "nvoi-myapp-prod"); err != nil {
		t.Fatalf("second: %v", err)
	}
}

func TestIgnoreNotFound(t *testing.T) {
	nf := apierrors.NewNotFound(schema.GroupResource{Resource: "pods"}, "x")
	if IgnoreNotFound(nf) != nil {
		t.Error("NotFound should be swallowed")
	}
	other := apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "x", nil)
	if IgnoreNotFound(other) == nil {
		t.Error("non-NotFound error should pass through")
	}
}

func TestPodSelector(t *testing.T) {
	got := PodSelector("web")
	want := "app.kubernetes.io/name=web"
	if got != want {
		t.Fatalf("PodSelector(%q) = %q, want %q", "web", got, want)
	}
}

// contains is a substring check used across test files.
func contains(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
