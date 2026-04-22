package kube

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/nvoi/pkg/utils"
)

// podLabel returns the label selector map WaitRollout's selector matches on.
func podLabel(service string) map[string]string {
	return map[string]string{utils.LabelAppName: service}
}

// readyPodFor returns a pod that is Running and Ready — WaitRollout's happy path.
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

// waitingPodFor returns a pod that is Running (pod phase) but its single
// container is Waiting with the given reason — common crash-loop shape.
func waitingPodFor(service, reason, message string, restartCount int32) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: service + "-abc", Namespace: "ns",
			Labels: podLabel(service),
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{
				Ready: false, RestartCount: restartCount,
				State: corev1.ContainerState{
					Waiting: &corev1.ContainerStateWaiting{Reason: reason, Message: message},
				},
			}},
		},
	}
}

func TestWaitRollout_ReadyImmediately(t *testing.T) {
	cleanup := fastTiming()
	defer cleanup()
	c := newTestClient(readyPodFor("web"))
	if err := c.WaitRollout(context.Background(), "ns", "web", "deployment", true, &testEmitter{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ── Unrecoverable states → bail immediately ──────────────────────────────

func TestWaitRollout_ImagePullBackOff_FailsFast(t *testing.T) {
	cleanup := fastTiming()
	defer cleanup()
	c := newTestClient(waitingPodFor("web", "ImagePullBackOff", "pull failed", 0))
	err := c.WaitRollout(context.Background(), "ns", "web", "deployment", true, &testEmitter{})
	if err == nil {
		t.Fatal("expected hard-fail on ImagePullBackOff")
	}
	if !contains(err.Error(), "ImagePullBackOff") {
		t.Errorf("error should name the reason, got: %q", err.Error())
	}
}

func TestWaitRollout_ErrImagePull_FailsFast(t *testing.T) {
	cleanup := fastTiming()
	defer cleanup()
	c := newTestClient(waitingPodFor("web", "ErrImagePull", "x", 0))
	if err := c.WaitRollout(context.Background(), "ns", "web", "deployment", true, &testEmitter{}); err == nil {
		t.Fatal("expected hard-fail on ErrImagePull")
	}
}

func TestWaitRollout_InvalidImageName_FailsFast(t *testing.T) {
	cleanup := fastTiming()
	defer cleanup()
	c := newTestClient(waitingPodFor("web", "InvalidImageName", "bad image", 0))
	if err := c.WaitRollout(context.Background(), "ns", "web", "deployment", true, &testEmitter{}); err == nil {
		t.Fatal("expected hard-fail on InvalidImageName")
	}
}

func TestWaitRollout_CreateContainerConfigError_FailsFast(t *testing.T) {
	cleanup := fastTiming()
	defer cleanup()
	c := newTestClient(waitingPodFor("web", "CreateContainerConfigError", "missing secret key X", 0))
	err := c.WaitRollout(context.Background(), "ns", "web", "deployment", true, &testEmitter{})
	if err == nil || !contains(err.Error(), "CreateContainerConfigError") {
		t.Fatalf("expected hard-fail naming the reason, got: %v", err)
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
		t.Fatalf("expected OOMKilled hard-fail, got: %v", err)
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

// ── Transient states → keep polling until outer timeout ──────────────────

func TestWaitRollout_CrashLoopBackOff_KeepsPolling(t *testing.T) {
	// CrashLoopBackOff is a transient state — kubelet is in its backoff
	// window between restarts, dep might still be coming up. WaitRollout
	// must NOT fail fast on it, no matter the restart count. The only exit
	// is the outer rolloutTimeout.
	cleanup := fastTiming()
	defer cleanup()

	tests := []struct{ restarts int32 }{{0}, {1}, {3}, {9}, {42}}
	for _, tt := range tests {
		c := newTestClient(waitingPodFor("web", "CrashLoopBackOff", "", tt.restarts))
		err := c.WaitRollout(context.Background(), "ns", "web", "deployment", true, &testEmitter{})
		if err == nil {
			t.Errorf("restarts=%d: expected timeout (never converged)", tt.restarts)
			continue
		}
		if !contains(err.Error(), "timed out") {
			t.Errorf("restarts=%d: CrashLoopBackOff must time out, not fail fast. got: %q", tt.restarts, err.Error())
		}
	}
}

func TestWaitRollout_ExitedError_KeepsPolling(t *testing.T) {
	// Plain `Error` exit (non-zero code, not OOMKilled) — app crashed.
	// Kubelet will restart; nvoi must wait.
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
				Ready: false, RestartCount: 3,
				State: corev1.ContainerState{
					Terminated: &corev1.ContainerStateTerminated{ExitCode: 1, Reason: "Error"},
				},
			}},
		},
	}
	c := newTestClient(pod)
	err := c.WaitRollout(context.Background(), "ns", "web", "deployment", true, &testEmitter{})
	if err == nil {
		t.Fatal("expected timeout")
	}
	if !contains(err.Error(), "timed out") {
		t.Errorf("Error exit must time out, not fail fast. got: %q", err.Error())
	}
}

func TestWaitRollout_ProbeFailing_KeepsPolling(t *testing.T) {
	// Container Running but readiness probe failing (e.g. app hasn't bound
	// its port yet). Standard convergence, must not fail fast.
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
				State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
			}},
		},
	}
	c := newTestClient(pod)
	err := c.WaitRollout(context.Background(), "ns", "web", "deployment", true, &testEmitter{})
	if err == nil {
		t.Fatal("expected timeout")
	}
	if !contains(err.Error(), "timed out") {
		t.Errorf("probe-failing must time out, not fail fast. got: %q", err.Error())
	}
}

func TestWaitRollout_EmptyPodList_TimesOut(t *testing.T) {
	// Pod hasn't been created yet — keep waiting.
	cleanup := fastTiming()
	defer cleanup()
	c := newTestClient()
	err := c.WaitRollout(context.Background(), "ns", "web", "deployment", true, &testEmitter{})
	if err == nil || !contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout, got: %v", err)
	}
}

func TestWaitRollout_NoHealthCheck_VerifiesStability(t *testing.T) {
	// hasHealthCheck=false → stability verification runs after all Ready.
	cleanup := fastTiming()
	defer cleanup()
	c := newTestClient(readyPodFor("web"))
	emitter := &testEmitter{}
	if err := c.WaitRollout(context.Background(), "ns", "web", "deployment", false, emitter); err != nil {
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

// ── hardFailReason — unit-level coverage of the classifier ───────────────

func TestHardFailReason_ReadyPod(t *testing.T) {
	if r := hardFailReason(readyPodFor("web")); r != "" {
		t.Errorf("ready pod must not be hard-fail: %s", r)
	}
}

func TestHardFailReason_CrashLoopBackOff_NotHardFail(t *testing.T) {
	// CrashLoopBackOff is transient by definition; kubelet restarts the
	// container. Must never classify as hard-fail regardless of count.
	for _, rc := range []int32{0, 1, 5, 50} {
		pod := waitingPodFor("web", "CrashLoopBackOff", "", rc)
		if r := hardFailReason(pod); r != "" {
			t.Errorf("restarts=%d: CrashLoopBackOff must not be hard-fail, got: %s", rc, r)
		}
	}
}

func TestHardFailReason_AllUnrecoverableReasonsDetected(t *testing.T) {
	unrecoverable := []string{
		"ImagePullBackOff",
		"ErrImagePull",
		"InvalidImageName",
		"ErrImageNeverPull",
		"CreateContainerConfigError",
	}
	for _, reason := range unrecoverable {
		pod := waitingPodFor("web", reason, "x", 0)
		if r := hardFailReason(pod); r == "" {
			t.Errorf("reason %q must be hard-fail", reason)
		}
	}
}

func TestHardFailReason_OOMKilled(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{{
				State: corev1.ContainerState{
					Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled"},
				},
			}},
		},
	}
	if r := hardFailReason(pod); r == "" {
		t.Error("OOMKilled must be hard-fail")
	}
}

func TestHardFailReason_Unschedulable(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{{
				Type: corev1.PodScheduled, Status: corev1.ConditionFalse,
				Reason: "Unschedulable",
			}},
		},
	}
	if r := hardFailReason(pod); r == "" {
		t.Error("Unschedulable must be hard-fail")
	}
}

func TestHardFailReason_PlainErrorExit_NotHardFail(t *testing.T) {
	// Exit code != 0 but not OOMKilled → kubelet will restart → transient.
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{{
				RestartCount: 10, // history is irrelevant
				State: corev1.ContainerState{
					Terminated: &corev1.ContainerStateTerminated{Reason: "Error", ExitCode: 1},
				},
			}},
		},
	}
	if r := hardFailReason(pod); r != "" {
		t.Errorf("plain Error exit must not be hard-fail, got: %s", r)
	}
}
