package core

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestServiceSet_MissingImage(t *testing.T) {
	err := ServiceSet(context.Background(), ServiceSetRequest{
		Cluster: testCluster(testKube()),
		Name:    "web",
		Image:   "",
	})
	if err == nil {
		t.Fatal("expected error for missing image")
	}
	if !strings.Contains(err.Error(), "--image is required") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestServiceSet_AppliesDeploymentAndService(t *testing.T) {
	kc := testKube()
	err := ServiceSet(context.Background(), ServiceSetRequest{
		Cluster:  testCluster(kc),
		Name:     "web",
		Image:    "nginx:latest",
		Port:     80,
		Replicas: 2,
	})
	if err != nil {
		t.Fatalf("ServiceSet: %v", err)
	}
	dep, err := kc.Clientset().AppsV1().Deployments("nvoi-myapp-prod").Get(context.Background(), "web", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("deployment not applied: %v", err)
	}
	if *dep.Spec.Replicas != 2 {
		t.Errorf("replicas = %d", *dep.Spec.Replicas)
	}
	svc, err := kc.Clientset().CoreV1().Services("nvoi-myapp-prod").Get(context.Background(), "web", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("service not applied: %v", err)
	}
	if svc.Spec.Ports[0].Port != 80 {
		t.Errorf("port = %d", svc.Spec.Ports[0].Port)
	}
}

func TestServiceDelete_Succeeds(t *testing.T) {
	kc := testKube(
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "nvoi-myapp-prod"}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "nvoi-myapp-prod"}},
	)

	err := ServiceDelete(context.Background(), ServiceDeleteRequest{
		Cluster: testCluster(kc),
		Name:    "web",
	})
	if err != nil {
		t.Fatalf("service delete should succeed: %v", err)
	}
	if _, err := kc.Clientset().AppsV1().Deployments("nvoi-myapp-prod").Get(context.Background(), "web", metav1.GetOptions{}); err == nil {
		t.Error("deployment should be gone")
	}
	if _, err := kc.Clientset().CoreV1().Services("nvoi-myapp-prod").Get(context.Background(), "web", metav1.GetOptions{}); err == nil {
		t.Error("service should be gone")
	}
}

func TestServiceDelete_Idempotent(t *testing.T) {
	kc := testKube()
	err := ServiceDelete(context.Background(), ServiceDeleteRequest{
		Cluster: testCluster(kc),
		Name:    "missing",
	})
	if err != nil {
		t.Fatalf("missing service: %v", err)
	}
}
