package kube

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/getnvoi/nvoi/internal/testutil"
)

func TestTimeoutDiagnostics_ProbeFailure(t *testing.T) {
	origTimeout := rolloutTimeout
	rolloutTimeout = 10 * time.Millisecond
	defer func() { rolloutTimeout = origTimeout }()

	// Pod is Running but not Ready — readiness probe failing. Never becomes ready.
	probeFailingJSON := `{
		"items": [{
			"metadata": {"name": "bugsink-abc"},
			"status": {
				"phase": "Running",
				"containerStatuses": [{
					"ready": false,
					"restartCount": 0,
					"state": {"running": {}}
				}]
			}
		}, {
			"metadata": {"name": "bugsink-def"},
			"status": {
				"phase": "Running",
				"containerStatuses": [{
					"ready": false,
					"restartCount": 0,
					"state": {"running": {}}
				}]
			}
		}]
	}`

	eventsJSON := `{
		"items": [{
			"type": "Warning",
			"reason": "Unhealthy",
			"message": "Readiness probe failed: HTTP probe failed with statuscode: 503",
			"count": 42
		}]
	}`

	ssh := testutil.NewMockSSH(nil)
	ssh.Prefixes = []testutil.MockPrefix{
		{Prefix: "get pods", Result: testutil.MockResult{Output: []byte(probeFailingJSON)}},
		{Prefix: "get events", Result: testutil.MockResult{Output: []byte(eventsJSON)}},
		{Prefix: "logs", Result: testutil.MockResult{Output: []byte("django.db.utils.OperationalError: could not connect to server")}},
	}

	emitter := &testEmitter{}
	err := WaitRollout(context.Background(), ssh, "default", "bugsink", "deployment", true, emitter)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	msg := err.Error()

	// Must contain the service name and "timed out"
	if !strings.Contains(msg, "bugsink") {
		t.Fatalf("expected error to contain service name 'bugsink', got:\n%s", msg)
	}
	if !strings.Contains(msg, "timed out") {
		t.Fatalf("expected error to contain 'timed out', got:\n%s", msg)
	}
	// Must show the pod state diagnosis
	if !strings.Contains(msg, "readiness probe failing") {
		t.Fatalf("expected error to mention 'readiness probe failing', got:\n%s", msg)
	}
	// Must include events
	if !strings.Contains(msg, "503") {
		t.Fatalf("expected error to include event details (503), got:\n%s", msg)
	}
	// Must include logs
	if !strings.Contains(msg, "could not connect to server") {
		t.Fatalf("expected error to include logs, got:\n%s", msg)
	}
	// During polling, status line should show probe failure with event detail
	found := false
	for _, m := range emitter.messages {
		if strings.Contains(m, "probe failing") && strings.Contains(m, "503") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected progress message with 'probe failing' and event detail (503), got: %v", emitter.messages)
	}
}

func TestTimeoutDiagnostics_Pending(t *testing.T) {
	origTimeout := rolloutTimeout
	rolloutTimeout = 10 * time.Millisecond
	defer func() { rolloutTimeout = origTimeout }()

	// Pod stuck in Pending with no container statuses (scheduling issue).
	pendingJSON := `{
		"items": [{
			"metadata": {"name": "web-abc"},
			"status": {
				"phase": "Pending",
				"conditions": [
					{"type": "PodScheduled", "status": "True", "reason": "", "message": ""}
				],
				"containerStatuses": []
			}
		}]
	}`

	ssh := testutil.NewMockSSH(nil)
	ssh.Prefixes = []testutil.MockPrefix{
		{Prefix: "get pods", Result: testutil.MockResult{Output: []byte(pendingJSON)}},
		{Prefix: "get events", Result: testutil.MockResult{Output: []byte(`{"items":[]}`)}},
		{Prefix: "logs", Result: testutil.MockResult{Output: []byte("")}},
	}

	emitter := &testEmitter{}
	err := WaitRollout(context.Background(), ssh, "default", "web", "deployment", false, emitter)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	msg := err.Error()

	if !strings.Contains(msg, "timed out") {
		t.Fatalf("expected 'timed out', got:\n%s", msg)
	}
	if !strings.Contains(msg, "Pending") {
		t.Fatalf("expected phase 'Pending' in diagnostics, got:\n%s", msg)
	}
}

func TestTimeoutDiagnostics_TerminatedWithRestarts(t *testing.T) {
	origTimeout := rolloutTimeout
	rolloutTimeout = 10 * time.Millisecond
	defer func() { rolloutTimeout = origTimeout }()

	// Pod terminated with exit code but somehow not caught by fast-fail
	// (e.g. restartCount=0, then pod terminates again during timeout window)
	terminatedJSON := `{
		"items": [{
			"metadata": {"name": "api-abc"},
			"status": {
				"phase": "Running",
				"containerStatuses": [{
					"ready": false,
					"restartCount": 3,
					"state": {"waiting": {"reason": "CrashLoopBackOff", "message": "back-off 5m0s restarting"}}
				}]
			}
		}]
	}`

	ssh := testutil.NewMockSSH(nil)
	ssh.Prefixes = []testutil.MockPrefix{
		// Polling phase: return empty so it keeps polling until timeout
		{Prefix: "get pods", Result: testutil.MockResult{Output: []byte(`{"items":[]}`)}},
		// Diagnostics phase will also match "get pods" — but MockSSH returns
		// the same prefix match. We need the diagnostics query to see the crashed pod.
	}

	// For this test, directly test the timeoutDiagnostics function
	ssh2 := testutil.NewMockSSH(nil)
	ssh2.Prefixes = []testutil.MockPrefix{
		{Prefix: "get pods", Result: testutil.MockResult{Output: []byte(terminatedJSON)}},
		{Prefix: "get events", Result: testutil.MockResult{Output: []byte(`{"items":[]}`)}},
		{Prefix: "logs", Result: testutil.MockResult{Output: []byte("panic: runtime error: nil pointer")}},
	}

	err := timeoutDiagnostics(context.Background(), ssh2, "default", "api", "deployment", "app.kubernetes.io/name=api", "0/1 ready (CrashLoopBackOff)")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	msg := err.Error()

	if !strings.Contains(msg, "timed out") {
		t.Fatalf("expected 'timed out', got:\n%s", msg)
	}
	if !strings.Contains(msg, "CrashLoopBackOff") {
		t.Fatalf("expected 'CrashLoopBackOff' in diagnostics, got:\n%s", msg)
	}
	if !strings.Contains(msg, "restarts: 3") {
		t.Fatalf("expected 'restarts: 3', got:\n%s", msg)
	}
	if !strings.Contains(msg, "nil pointer") {
		t.Fatalf("expected logs in diagnostics, got:\n%s", msg)
	}
}

func TestTimeoutDiagnostics_SSHFailure(t *testing.T) {
	// When SSH fails during diagnostics, still return a useful error (just less detail)
	err := timeoutDiagnostics(
		context.Background(),
		testutil.NewMockSSH(nil), // no prefixes configured — all commands fail
		"default", "web", "deployment",
		"app.kubernetes.io/name=web", "0/2 ready",
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "timed out") {
		t.Fatalf("expected 'timed out' even on SSH failure, got:\n%s", msg)
	}
	if !strings.Contains(msg, "0/2 ready") {
		t.Fatalf("expected last status in error, got:\n%s", msg)
	}
}

func TestPodReady(t *testing.T) {
	tests := []struct {
		name string
		pod  PodItem
		want bool
	}{
		{
			name: "running and ready",
			pod: PodItem{
				Status: PodStatus{
					Phase: "Running",
					ContainerStatuses: []ContainerStatus{
						{Ready: true, State: ContainerState{Running: &struct{}{}}},
					},
				},
			},
			want: true,
		},
		{
			name: "running not ready",
			pod: PodItem{
				Status: PodStatus{
					Phase: "Running",
					ContainerStatuses: []ContainerStatus{
						{Ready: false, State: ContainerState{Running: &struct{}{}}},
					},
				},
			},
			want: false,
		},
		{
			name: "pending",
			pod: PodItem{
				Status: PodStatus{
					Phase:             "Pending",
					ContainerStatuses: []ContainerStatus{},
				},
			},
			want: false,
		},
		{
			name: "succeeded",
			pod: PodItem{
				Status: PodStatus{
					Phase: "Succeeded",
					ContainerStatuses: []ContainerStatus{
						{Ready: true},
					},
				},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := podReady(tt.pod)
			if got != tt.want {
				t.Fatalf("podReady() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRecentEvents(t *testing.T) {
	eventsJSON := `{
		"items": [
			{"type": "Warning", "reason": "Unhealthy", "message": "Readiness probe failed: connection refused", "count": 5},
			{"type": "Warning", "reason": "BackOff", "message": "Back-off pulling image", "count": 1}
		]
	}`

	ssh := testutil.NewMockSSH(nil)
	ssh.Prefixes = []testutil.MockPrefix{
		{Prefix: "get events", Result: testutil.MockResult{Output: []byte(eventsJSON)}},
	}

	events := recentEvents(context.Background(), ssh, "default", "web-abc")
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Reason != "Unhealthy" {
		t.Fatalf("expected first event reason 'Unhealthy', got %q", events[0].Reason)
	}
	if events[0].Count != 5 {
		t.Fatalf("expected first event count 5, got %d", events[0].Count)
	}
}

func TestRecentEvents_SSHFailure(t *testing.T) {
	ssh := testutil.NewMockSSH(nil) // no prefixes — all commands return nil
	events := recentEvents(context.Background(), ssh, "default", "web-abc")
	if events != nil {
		t.Fatalf("expected nil events on SSH failure, got %v", events)
	}
}
