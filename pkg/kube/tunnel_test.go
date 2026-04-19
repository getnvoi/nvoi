package kube

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

func TestPurgeTunnelAgents_Empty_Idempotent(t *testing.T) {
	cs := k8sfake.NewSimpleClientset()
	c := NewForTest(cs)
	if err := c.PurgeTunnelAgents(context.Background(), "test-ns"); err != nil {
		t.Fatalf("PurgeTunnelAgents on empty cluster: %v", err)
	}
}

func TestPurgeTunnelAgents_RemovesCloudflaredAndNgrok(t *testing.T) {
	ns := "nvoi-myapp-prod"
	cs := k8sfake.NewSimpleClientset(
		// cloudflared workloads
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: CloudflareTunnelAgentName, Namespace: ns}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: CloudflareTunnelSecretName, Namespace: ns}},
		// ngrok workloads
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: NgrokTunnelAgentName, Namespace: ns}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: NgrokTunnelSecretName, Namespace: ns}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: NgrokTunnelConfigMapName, Namespace: ns}},
	)
	c := NewForTest(cs)

	if err := c.PurgeTunnelAgents(context.Background(), ns); err != nil {
		t.Fatalf("PurgeTunnelAgents: %v", err)
	}

	for _, name := range []string{CloudflareTunnelAgentName, NgrokTunnelAgentName} {
		if _, err := cs.AppsV1().Deployments(ns).Get(context.Background(), name, metav1.GetOptions{}); err == nil {
			t.Errorf("deployment/%s should be gone", name)
		}
	}
	for _, name := range []string{CloudflareTunnelSecretName, NgrokTunnelSecretName} {
		if _, err := cs.CoreV1().Secrets(ns).Get(context.Background(), name, metav1.GetOptions{}); err == nil {
			t.Errorf("secret/%s should be gone", name)
		}
	}
	if _, err := cs.CoreV1().ConfigMaps(ns).Get(context.Background(), NgrokTunnelConfigMapName, metav1.GetOptions{}); err == nil {
		t.Errorf("configmap/%s should be gone", NgrokTunnelConfigMapName)
	}
}

func TestPurgeTunnelAgents_PartialState_Idempotent(t *testing.T) {
	ns := "nvoi-myapp-prod"
	// Only cloudflared exists — ngrok resources absent.
	cs := k8sfake.NewSimpleClientset(
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: CloudflareTunnelAgentName, Namespace: ns}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: CloudflareTunnelSecretName, Namespace: ns}},
	)
	c := NewForTest(cs)

	if err := c.PurgeTunnelAgents(context.Background(), ns); err != nil {
		t.Fatalf("PurgeTunnelAgents partial state: %v", err)
	}
}

func TestGetTunnelAgentPods_ReturnsCloudflaredPods(t *testing.T) {
	ns := "nvoi-myapp-prod"
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cloudflared-abc123",
			Namespace: ns,
			Labels:    map[string]string{"app.kubernetes.io/name": CloudflareTunnelAgentName},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	cs := k8sfake.NewSimpleClientset(pod)
	c := NewForTest(cs)

	pods, err := c.GetTunnelAgentPods(context.Background(), ns)
	if err != nil {
		t.Fatalf("GetTunnelAgentPods: %v", err)
	}
	if len(pods) != 1 {
		t.Fatalf("len = %d, want 1", len(pods))
	}
	if pods[0].Name != "cloudflared-abc123" {
		t.Errorf("Name = %q", pods[0].Name)
	}
}

func TestGetTunnelAgentPods_ReturnsNgrokPods(t *testing.T) {
	ns := "nvoi-myapp-prod"
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ngrok-xyz",
			Namespace: ns,
			Labels:    map[string]string{"app.kubernetes.io/name": NgrokTunnelAgentName},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	cs := k8sfake.NewSimpleClientset(pod)
	c := NewForTest(cs)

	pods, err := c.GetTunnelAgentPods(context.Background(), ns)
	if err != nil {
		t.Fatalf("GetTunnelAgentPods ngrok: %v", err)
	}
	if len(pods) != 1 || pods[0].Name != "ngrok-xyz" {
		t.Errorf("got %+v", pods)
	}
}

func TestGetTunnelAgentPods_EmptyWhenNone(t *testing.T) {
	cs := k8sfake.NewSimpleClientset()
	c := NewForTest(cs)
	pods, err := c.GetTunnelAgentPods(context.Background(), "nvoi-myapp-prod")
	if err != nil {
		t.Fatalf("GetTunnelAgentPods empty: %v", err)
	}
	if len(pods) != 0 {
		t.Errorf("expected 0 pods, got %d", len(pods))
	}
}

func TestGetTunnelAgentPods_FiltersUnrelatedPods(t *testing.T) {
	ns := "nvoi-myapp-prod"
	cs := k8sfake.NewSimpleClientset(
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "unrelated-pod", Namespace: ns,
				Labels: map[string]string{"app.kubernetes.io/name": "some-other-app"},
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "cloudflared-real", Namespace: ns,
				Labels: map[string]string{"app.kubernetes.io/name": CloudflareTunnelAgentName},
			},
		},
	)
	c := NewForTest(cs)

	pods, err := c.GetTunnelAgentPods(context.Background(), ns)
	if err != nil {
		t.Fatalf("GetTunnelAgentPods: %v", err)
	}
	if len(pods) != 1 || pods[0].Name != "cloudflared-real" {
		t.Errorf("expected only cloudflared-real; got %+v", pods)
	}
}

func TestPurgeCaddy_Empty_Idempotent(t *testing.T) {
	cs := k8sfake.NewSimpleClientset()
	c := NewForTest(cs)
	if err := c.PurgeCaddy(context.Background()); err != nil {
		t.Fatalf("PurgeCaddy on empty cluster: %v", err)
	}
}

func TestPurgeCaddy_RemovesAllResources(t *testing.T) {
	cs := k8sfake.NewSimpleClientset(
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: CaddyName, Namespace: CaddyNamespace}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: CaddyName, Namespace: CaddyNamespace}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: CaddyConfigMapName, Namespace: CaddyNamespace}},
		&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: CaddyPVCName, Namespace: CaddyNamespace}},
	)
	c := NewForTest(cs)

	if err := c.PurgeCaddy(context.Background()); err != nil {
		t.Fatalf("PurgeCaddy: %v", err)
	}

	if _, err := cs.AppsV1().Deployments(CaddyNamespace).Get(context.Background(), CaddyName, metav1.GetOptions{}); err == nil {
		t.Error("caddy deployment should be gone")
	}
	if _, err := cs.CoreV1().Services(CaddyNamespace).Get(context.Background(), CaddyName, metav1.GetOptions{}); err == nil {
		t.Error("caddy service should be gone")
	}
	if _, err := cs.CoreV1().ConfigMaps(CaddyNamespace).Get(context.Background(), CaddyConfigMapName, metav1.GetOptions{}); err == nil {
		t.Error("caddy configmap should be gone")
	}
	if _, err := cs.CoreV1().PersistentVolumeClaims(CaddyNamespace).Get(context.Background(), CaddyPVCName, metav1.GetOptions{}); err == nil {
		t.Error("caddy pvc should be gone")
	}
}
