package kube

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/nvoi/pkg/utils"
)

func TestFirstPod_FirstMatchWins(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web-abc",
			Namespace: "ns",
			Labels:    map[string]string{utils.LabelAppName: "web"},
		},
	}
	c := newTestClient(pod)

	got, err := c.FirstPod(context.Background(), "ns", "web")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "web-abc" {
		t.Errorf("got %q, want web-abc", got)
	}
}

func TestFirstPod_NoMatch(t *testing.T) {
	c := newTestClient()
	_, err := c.FirstPod(context.Background(), "ns", "web")
	if err == nil {
		t.Fatal("expected error for missing pod")
	}
	if !contains(err.Error(), "no pods found") {
		t.Errorf("error = %q, want 'no pods found'", err.Error())
	}
}

func TestGetServicePort_FirstPort(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "ns"},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{Port: 3000}, {Port: 9090}},
		},
	}
	c := newTestClient(svc)

	port, err := c.GetServicePort(context.Background(), "ns", "web")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if port != 3000 {
		t.Errorf("port = %d, want 3000", port)
	}
}

func TestGetServicePort_NotFound(t *testing.T) {
	c := newTestClient()
	_, err := c.GetServicePort(context.Background(), "ns", "missing")
	if err == nil || !contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got: %v", err)
	}
}

func TestGetServicePort_NoPorts(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "ns"},
	}
	c := newTestClient(svc)

	_, err := c.GetServicePort(context.Background(), "ns", "web")
	if err == nil || !contains(err.Error(), "no port") {
		t.Fatalf("expected no-port error, got: %v", err)
	}
}

func TestDeleteByName_DeploymentAndService(t *testing.T) {
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "ns"}}
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "ns"}}
	c := newTestClient(dep, svc)

	if err := c.DeleteByName(context.Background(), "ns", "web"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := c.cs.AppsV1().Deployments("ns").Get(context.Background(), "web", metav1.GetOptions{}); err == nil {
		t.Error("deployment should be gone")
	}
	if _, err := c.cs.CoreV1().Services("ns").Get(context.Background(), "web", metav1.GetOptions{}); err == nil {
		t.Error("service should be gone")
	}
}

func TestDeleteByName_StatefulSet(t *testing.T) {
	ss := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "ns"}}
	c := newTestClient(ss)

	if err := c.DeleteByName(context.Background(), "ns", "db"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := c.cs.AppsV1().StatefulSets("ns").Get(context.Background(), "db", metav1.GetOptions{}); err == nil {
		t.Error("statefulset should be gone")
	}
}

func TestDeleteByName_AllAbsent_Idempotent(t *testing.T) {
	c := newTestClient()
	if err := c.DeleteByName(context.Background(), "ns", "web"); err != nil {
		t.Fatalf("idempotent delete: %v", err)
	}
}
