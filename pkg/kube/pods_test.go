package kube

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func readyPod(name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns"},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: name, Ready: true,
				State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
			}},
		},
	}
}

func TestGetAllPods_AllReady(t *testing.T) {
	c := newTestClient(readyPod("web"), readyPod("db"))
	got, err := c.GetAllPods(context.Background(), "ns")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("count = %d, want 2", len(got))
	}
	for _, p := range got {
		if !p.Ready {
			t.Errorf("%s not Ready", p.Name)
		}
		if p.Status != "Running" {
			t.Errorf("%s status = %q, want Running", p.Name, p.Status)
		}
	}
}

func TestGetAllPods_Empty(t *testing.T) {
	c := newTestClient()
	got, err := c.GetAllPods(context.Background(), "ns")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("count = %d, want 0", len(got))
	}
}

func TestGetAllPods_ContainerCreating(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "ns"},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			ContainerStatuses: []corev1.ContainerStatus{{
				Ready: false,
				State: corev1.ContainerState{
					Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating"},
				},
			}},
		},
	}
	c := newTestClient(pod)
	got, err := c.GetAllPods(context.Background(), "ns")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].Ready || got[0].Status != "ContainerCreating" {
		t.Fatalf("got %+v", got)
	}
}

func TestGetAllPods_CrashLoop(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "ns"},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{
				Ready: false,
				State: corev1.ContainerState{
					Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"},
				},
			}},
		},
	}
	c := newTestClient(pod)
	got, err := c.GetAllPods(context.Background(), "ns")
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Ready || got[0].Status != "CrashLoopBackOff" {
		t.Errorf("got %+v", got[0])
	}
}

func TestGetAllPods_MixedState(t *testing.T) {
	ready := readyPod("web")
	pending := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "ns"},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			ContainerStatuses: []corev1.ContainerStatus{{
				Ready: false,
				State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"}},
			}},
		},
	}
	c := newTestClient(ready, pending)

	got, err := c.GetAllPods(context.Background(), "ns")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("count = %d", len(got))
	}
	// Find each by name — fake clientset doesn't guarantee order.
	byName := map[string]PodInfo{}
	for _, p := range got {
		byName[p.Name] = p
	}
	if !byName["web"].Ready {
		t.Error("web should be ready")
	}
	if byName["db"].Ready {
		t.Error("db should not be ready")
	}
	if byName["db"].Status != "ImagePullBackOff" {
		t.Errorf("db status = %q", byName["db"].Status)
	}
}
