package kube

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// podLabel returns the label selector map WaitRollout's selector matches on.
func podLabel(service string) map[string]string {
	return map[string]string{"app.kubernetes.io/name": service}
}

func readyPodFor(service string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: service + "-abc", Namespace: "ns",
			Labels: podLabel(service),
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{
				Ready: true,
				State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
			}},
		},
	}
}

func TestWaitRollout_ReadyImmediately(t *testing.T) {
	cleanup := fastTiming()
	defer cleanup()
	c := newTestClient(readyPodFor("web"))
	emitter := &testEmitter{}

	// hasHealthCheck=true skips the stability phase (keeps tests quick+deterministic).
	err := c.WaitRollout(context.Background(), "ns", "web", "deployment", true, emitter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWaitRollout_ImagePullBackOff_FailsFast(t *testing.T) {
	cleanup := fastTiming()
	defer cleanup()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-abc", Namespace: "ns",
			Labels: podLabel("web"),
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			ContainerStatuses: []corev1.ContainerStatus{{
				Ready: false,
				State: corev1.ContainerState{
					Waiting: &corev1.ContainerStateWaiting{
						Reason: "ImagePullBackOff", Message: "pull failed",
					},
				},
			}},
		},
	}
	c := newTestClient(pod)
	err := c.WaitRollout(context.Background(), "ns", "web", "deployment", true, &testEmitter{})
	if err == nil {
		t.Fatal("expected ImagePullBackOff error")
	}
	if !contains(err.Error(), "ImagePullBackOff") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestWaitRollout_CrashLoopBackOff_FailsFast(t *testing.T) {
	cleanup := fastTiming()
	defer cleanup()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-abc", Namespace: "ns",
			Labels: podLabel("web"),
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{
				Ready: false, RestartCount: 5,
				State: corev1.ContainerState{
					Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"},
				},
			}},
		},
	}
	c := newTestClient(pod)
	err := c.WaitRollout(context.Background(), "ns", "web", "deployment", true, &testEmitter{})
	if err == nil {
		t.Fatal("expected CrashLoopBackOff error")
	}
	if !contains(err.Error(), "CrashLoopBackOff") || !contains(err.Error(), "restarts: 5") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestWaitRollout_OOMKilled_FailsFast(t *testing.T) {
	cleanup := fastTiming()
	defer cleanup()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-abc", Namespace: "ns",
			Labels: podLabel("web"),
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{
				Ready: false,
				State: corev1.ContainerState{
					Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled", ExitCode: 137},
				},
			}},
		},
	}
	c := newTestClient(pod)
	err := c.WaitRollout(context.Background(), "ns", "web", "deployment", true, &testEmitter{})
	if err == nil || !contains(err.Error(), "OOMKilled") {
		t.Fatalf("expected OOMKilled error, got: %v", err)
	}
}

func TestWaitRollout_Unschedulable_FailsFast(t *testing.T) {
	cleanup := fastTiming()
	defer cleanup()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-abc", Namespace: "ns",
			Labels: podLabel("web"),
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			Conditions: []corev1.PodCondition{{
				Type: corev1.PodScheduled, Status: corev1.ConditionFalse,
				Reason:  "Unschedulable",
				Message: "0/1 nodes available: 1 Insufficient memory",
			}},
		},
	}
	c := newTestClient(pod)
	err := c.WaitRollout(context.Background(), "ns", "web", "deployment", true, &testEmitter{})
	if err == nil {
		t.Fatal("expected unschedulable error")
	}
	if !contains(err.Error(), "unschedulable") && !contains(err.Error(), "Unschedulable") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestWaitRollout_EmptyPodList_TimesOut(t *testing.T) {
	cleanup := fastTiming()
	defer cleanup()
	c := newTestClient()
	err := c.WaitRollout(context.Background(), "ns", "web", "deployment", true, &testEmitter{})
	if err == nil {
		t.Fatal("expected timeout when no pods match selector")
	}
	// timeoutDiagnostics wraps the error with the "timed out" marker.
	if !contains(err.Error(), "timed out") {
		t.Errorf("expected timeout error, got: %s", err.Error())
	}
}

func TestWaitRollout_NoHealthCheck_VerifiesStability(t *testing.T) {
	cleanup := fastTiming()
	defer cleanup()
	c := newTestClient(readyPodFor("web"))
	emitter := &testEmitter{}

	// hasHealthCheck=false runs the stability pass.
	err := c.WaitRollout(context.Background(), "ns", "web", "deployment", false, emitter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var sawStability bool
	for _, m := range emitter.all() {
		if contains(m, "verifying stability") {
			sawStability = true
		}
	}
	if !sawStability {
		t.Errorf("expected 'verifying stability' progress, got: %v", emitter.all())
	}
}
