package kube

import (
	"context"
	"io"
	"io/fs"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/getnvoi/nvoi/internal/testutil"
	"github.com/getnvoi/nvoi/pkg/core"
)

// testEmitter collects Progress messages for assertions.
type testEmitter struct {
	mu       sync.Mutex
	messages []string
}

func (e *testEmitter) Progress(msg string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.messages = append(e.messages, msg)
}

// --- WaitRollout tests ---

func TestWaitRollout_AllReady(t *testing.T) {
	podsJSON := `{
		"items": [
			{
				"metadata": {"name": "web-abc"},
				"status": {
					"phase": "Running",
					"containerStatuses": [{"ready": true, "restartCount": 0, "state": {"running": {}}}]
				}
			},
			{
				"metadata": {"name": "web-def"},
				"status": {
					"phase": "Running",
					"containerStatuses": [{"ready": true, "restartCount": 0, "state": {"running": {}}}]
				}
			}
		]
	}`

	ssh := testutil.NewMockSSH(nil)
	ssh.Prefixes = []testutil.MockPrefix{
		{Prefix: "get pods", Result: testutil.MockResult{Output: []byte(podsJSON)}},
	}

	emitter := &testEmitter{}
	err := WaitRollout(context.Background(), ssh, "default", "web", "deployment", emitter)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
}

func TestWaitRollout_ImagePullBackOff(t *testing.T) {
	podsJSON := `{
		"items": [
			{
				"metadata": {"name": "web-abc"},
				"status": {
					"phase": "Pending",
					"containerStatuses": [{
						"ready": false,
						"restartCount": 0,
						"state": {"waiting": {"reason": "ImagePullBackOff", "message": "back-off pulling image \"bad:latest\""}}
					}]
				}
			}
		]
	}`

	ssh := testutil.NewMockSSH(nil)
	ssh.Prefixes = []testutil.MockPrefix{
		{Prefix: "get pods", Result: testutil.MockResult{Output: []byte(podsJSON)}},
	}

	emitter := &testEmitter{}
	err := WaitRollout(context.Background(), ssh, "default", "web", "deployment", emitter)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "ImagePullBackOff") {
		t.Fatalf("expected error to contain 'ImagePullBackOff', got: %v", err)
	}
}

func TestWaitRollout_CrashLoopBackOff(t *testing.T) {
	podsJSON := `{
		"items": [
			{
				"metadata": {"name": "web-abc"},
				"status": {
					"phase": "Running",
					"containerStatuses": [{
						"ready": false,
						"restartCount": 5,
						"state": {"waiting": {"reason": "CrashLoopBackOff", "message": "back-off 5m0s restarting failed container"}}
					}]
				}
			}
		]
	}`

	ssh := testutil.NewMockSSH(nil)
	ssh.Prefixes = []testutil.MockPrefix{
		{Prefix: "get pods", Result: testutil.MockResult{Output: []byte(podsJSON)}},
		{Prefix: "logs", Result: testutil.MockResult{Output: []byte("Error: cannot connect to database\nExiting with code 1")}},
	}

	emitter := &testEmitter{}
	err := WaitRollout(context.Background(), ssh, "default", "web", "deployment", emitter)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "CrashLoopBackOff") {
		t.Fatalf("expected error to contain 'CrashLoopBackOff', got: %v", err)
	}
	if !strings.Contains(err.Error(), "restarts: 5") {
		t.Fatalf("expected error to contain 'restarts: 5', got: %v", err)
	}
}

func TestWaitRollout_OOMKilled(t *testing.T) {
	podsJSON := `{
		"items": [
			{
				"metadata": {"name": "web-abc"},
				"status": {
					"phase": "Running",
					"containerStatuses": [{
						"ready": false,
						"restartCount": 1,
						"state": {"terminated": {"reason": "OOMKilled", "message": ""}}
					}]
				}
			}
		]
	}`

	ssh := testutil.NewMockSSH(nil)
	ssh.Prefixes = []testutil.MockPrefix{
		{Prefix: "get pods", Result: testutil.MockResult{Output: []byte(podsJSON)}},
	}

	emitter := &testEmitter{}
	err := WaitRollout(context.Background(), ssh, "default", "web", "deployment", emitter)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "OOMKilled") {
		t.Fatalf("expected error to contain 'OOMKilled', got: %v", err)
	}
}

func TestWaitRollout_Unschedulable(t *testing.T) {
	podsJSON := `{
		"items": [
			{
				"metadata": {"name": "web-abc"},
				"status": {
					"phase": "Pending",
					"conditions": [
						{"type": "PodScheduled", "status": "False", "reason": "Unschedulable", "message": "0/1 nodes are available: insufficient cpu"}
					],
					"containerStatuses": []
				}
			}
		]
	}`

	ssh := testutil.NewMockSSH(nil)
	ssh.Prefixes = []testutil.MockPrefix{
		{Prefix: "get pods", Result: testutil.MockResult{Output: []byte(podsJSON)}},
	}

	emitter := &testEmitter{}
	err := WaitRollout(context.Background(), ssh, "default", "web", "deployment", emitter)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "unschedulable") {
		t.Fatalf("expected error to contain 'unschedulable', got: %v", err)
	}
}

func TestWaitRollout_TransientThenReady(t *testing.T) {
	containerCreatingJSON := `{
		"items": [
			{
				"metadata": {"name": "web-abc"},
				"status": {
					"phase": "Pending",
					"containerStatuses": [{
						"ready": false,
						"restartCount": 0,
						"state": {"waiting": {"reason": "ContainerCreating", "message": ""}}
					}]
				}
			}
		]
	}`
	readyJSON := `{
		"items": [
			{
				"metadata": {"name": "web-abc"},
				"status": {
					"phase": "Running",
					"containerStatuses": [{"ready": true, "restartCount": 0, "state": {"running": {}}}]
				}
			}
		]
	}`

	counter := &counterSSH{
		responses: []testutil.MockResult{
			{Output: []byte(containerCreatingJSON)},
			{Output: []byte(readyJSON)},
		},
	}

	emitter := &testEmitter{}
	err := WaitRollout(context.Background(), counter, "default", "web", "deployment", emitter)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
}

// counterSSH is a minimal core.SSHClient that cycles through canned responses
// for commands containing "get pods". All other methods are no-op stubs.
type counterSSH struct {
	mu        sync.Mutex
	callCount int
	responses []testutil.MockResult
}

func (c *counterSSH) Run(_ context.Context, cmd string) ([]byte, error) {
	if strings.Contains(cmd, "get pods") {
		c.mu.Lock()
		idx := c.callCount
		if idx >= len(c.responses) {
			idx = len(c.responses) - 1
		}
		c.callCount++
		c.mu.Unlock()
		return c.responses[idx].Output, c.responses[idx].Err
	}
	return nil, nil
}

func (c *counterSSH) RunStream(_ context.Context, _ string, _, _ io.Writer) error {
	return nil
}

func (c *counterSSH) Upload(_ context.Context, _ io.Reader, _ string, _ fs.FileMode) error {
	return nil
}

func (c *counterSSH) Stat(_ context.Context, _ string) (*core.RemoteFileInfo, error) {
	return nil, nil
}

func (c *counterSSH) DialTCP(_ context.Context, _ string) (net.Conn, error) {
	return nil, nil
}

func (c *counterSSH) Close() error {
	return nil
}

var _ core.SSHClient = (*counterSSH)(nil)

// --- Helper function tests ---

func TestDedup(t *testing.T) {
	input := []string{"a", "b", "a", "c"}
	got := dedup(input)
	want := []string{"a", "b", "c"}

	if len(got) != len(want) {
		t.Fatalf("dedup(%v) = %v, want %v", input, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("dedup(%v)[%d] = %q, want %q", input, i, got[i], want[i])
		}
	}
}

func TestIndent(t *testing.T) {
	input := "line1\nline2"
	got := indent(input, "  ")
	want := "  line1\n  line2"
	if got != want {
		t.Fatalf("indent(%q, \"  \") = %q, want %q", input, got, want)
	}
}
