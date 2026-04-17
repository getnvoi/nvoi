package kube

import (
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/getnvoi/nvoi/pkg/utils"
)

// newTestClient returns a Client backed by a client-go fake clientset,
// pre-populated with objs. No SSH tunnel — Close() is a no-op. Exec must be
// wired by the test via c.ExecFunc.
func newTestClient(objs ...runtime.Object) *Client {
	cs := k8sfake.NewSimpleClientset(objs...)
	return NewForTest(cs)
}

func mustNames(t *testing.T) *utils.Names {
	t.Helper()
	n, err := utils.NewNames("myapp", "production")
	if err != nil {
		t.Fatalf("NewNames: %v", err)
	}
	return n
}

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

func (e *testEmitter) all() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, len(e.messages))
	copy(out, e.messages)
	return out
}

// fastTiming shrinks rollout polling so tests finish under the bin/test
// 2s-per-package budget. Restored by the returned cleanup.
func fastTiming() func() {
	origPoll := rolloutPollInterval
	origStability := stabilityDelay
	origTimeout := rolloutTimeout
	SetTestTiming(time.Millisecond, time.Millisecond)
	rolloutTimeout = 100 * time.Millisecond
	return func() {
		rolloutPollInterval = origPoll
		stabilityDelay = origStability
		rolloutTimeout = origTimeout
	}
}
