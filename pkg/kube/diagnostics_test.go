package kube

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPodReady_AllReady(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{Ready: true}, {Ready: true},
			},
		},
	}
	if !podReady(pod) {
		t.Error("all-ready running pod must be ready")
	}
}

func TestPodReady_OneContainerNotReady(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{Ready: true}, {Ready: false},
			},
		},
	}
	if podReady(pod) {
		t.Error("one-not-ready pod must not be ready")
	}
}

func TestPodReady_Pending(t *testing.T) {
	pod := &corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodPending}}
	if podReady(pod) {
		t.Error("Pending pod must not be ready")
	}
}

func TestPodReady_SucceededIsReady(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			Phase:             corev1.PodSucceeded,
			ContainerStatuses: []corev1.ContainerStatus{{Ready: true}},
		},
	}
	if !podReady(pod) {
		t.Error("Succeeded with all-ready containers must be ready")
	}
}

func TestPodReady_NoContainerStatuses(t *testing.T) {
	pod := &corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodRunning}}
	if podReady(pod) {
		t.Error("no container statuses must not count as ready")
	}
}

func TestRecentEvents_FiltersWarnings(t *testing.T) {
	warnEvent := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{Name: "evt1", Namespace: "ns"},
		InvolvedObject: corev1.ObjectReference{
			Kind: "Pod", Name: "web-abc", Namespace: "ns",
		},
		Type:    corev1.EventTypeWarning,
		Reason:  "Unhealthy",
		Message: "Readiness probe failed",
	}
	c := newTestClient(warnEvent)

	got := c.recentEvents(context.Background(), "ns", "web-abc")
	if len(got) != 1 {
		t.Fatalf("events = %d, want 1", len(got))
	}
	if got[0].Message != "Readiness probe failed" {
		t.Errorf("message = %q", got[0].Message)
	}
}

func TestRecentEvents_NoEvents(t *testing.T) {
	c := newTestClient()
	got := c.recentEvents(context.Background(), "ns", "web-abc")
	if len(got) != 0 {
		t.Errorf("events = %d, want 0", len(got))
	}
}

func TestTimeoutDiagnostics_IncludesPodPhase(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web-abc",
			Namespace: "ns",
			Labels:    map[string]string{"app.kubernetes.io/name": "web"},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			ContainerStatuses: []corev1.ContainerStatus{{
				Ready: false,
				State: corev1.ContainerState{
					Waiting: &corev1.ContainerStateWaiting{
						Reason: "ImagePullBackOff", Message: "image not found",
					},
				},
			}},
		},
	}
	c := newTestClient(pod)

	err := c.timeoutDiagnostics(context.Background(), "ns", "web", "deployment", PodSelector("web"), "0/1 ready")
	if err == nil {
		t.Fatal("timeoutDiagnostics must always return an error")
	}
	msg := err.Error()
	for _, want := range []string{"timed out", "web-abc", "ImagePullBackOff", "image not found"} {
		if !contains(msg, want) {
			t.Errorf("error missing %q: %s", want, msg)
		}
	}
}

func TestTimeoutDiagnostics_RunningButUnready_FlagsProbe(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-abc", Namespace: "ns",
			Labels: map[string]string{"app.kubernetes.io/name": "web"},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{
				Ready: false,
				State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
			}},
		},
	}
	c := newTestClient(pod)

	err := c.timeoutDiagnostics(context.Background(), "ns", "web", "deployment", PodSelector("web"), "0/1 ready")
	if err == nil {
		t.Fatal("expected error")
	}
	if !contains(err.Error(), "readiness probe failing") {
		t.Errorf("expected probe-failing hint, got: %s", err.Error())
	}
}

func TestTimeoutDiagnostics_Terminated_ShowsExitCode(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-abc", Namespace: "ns",
			Labels: map[string]string{"app.kubernetes.io/name": "web"},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{
				Ready:        false,
				RestartCount: 3,
				State: corev1.ContainerState{
					Terminated: &corev1.ContainerStateTerminated{
						ExitCode: 137, Reason: "OOMKilled",
					},
				},
			}},
		},
	}
	c := newTestClient(pod)

	err := c.timeoutDiagnostics(context.Background(), "ns", "web", "deployment", PodSelector("web"), "0/1 ready")
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !contains(msg, "exit 137") || !contains(msg, "OOMKilled") {
		t.Errorf("expected exit+reason, got: %s", msg)
	}
	if !contains(msg, "restarts: 3") {
		t.Errorf("expected restart count, got: %s", msg)
	}
}

func TestIndent(t *testing.T) {
	got := indent("a\nb\nc", "> ")
	want := "> a\n> b\n> c"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDedup(t *testing.T) {
	got := dedup([]string{"a", "b", "a", "c", "b"})
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("dedup = %v", got)
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("hello world", 5); got != "hello..." {
		t.Errorf("truncate short = %q", got)
	}
	if got := truncate("short", 100); got != "short" {
		t.Errorf("truncate no-op = %q", got)
	}
}
