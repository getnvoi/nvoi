package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/testutil"
)

func init() {
	reporterFlushEvery = 50 * time.Millisecond
	reporterBackoffMin = 10 * time.Millisecond
	reporterBackoffMax = 50 * time.Millisecond
}

// ── Reporter tests ─────────────────────────────────────────────────────────

func TestReporter_NilWhenNoURL(t *testing.T) {
	r := NewReporter("", "token", "app", "prod")
	if r != nil {
		t.Fatal("expected nil reporter when URL is empty")
	}
}

func TestReporter_SendsEvents(t *testing.T) {
	var mu sync.Mutex
	var received []json.RawMessage

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		received = append(received, data)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := NewReporter(srv.URL, "tok", "myapp", "prod")
	r.Send(app.NewMessageEvent(app.EventSuccess, "server created"))
	r.Send(app.NewMessageEvent(app.EventProgress, "installing docker"))
	r.Close()

	mu.Lock()
	defer mu.Unlock()

	if len(received) == 0 {
		t.Fatal("expected at least one batch POST")
	}

	// Parse the last batch to verify structure.
	var payload struct {
		App    string      `json:"app"`
		Env    string      `json:"env"`
		Events []app.Event `json:"events"`
	}
	if err := json.Unmarshal(received[len(received)-1], &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.App != "myapp" || payload.Env != "prod" {
		t.Errorf("app/env = %s/%s, want myapp/prod", payload.App, payload.Env)
	}
	// All events should have been flushed across all batches.
	total := 0
	for _, raw := range received {
		var p struct{ Events []app.Event }
		json.Unmarshal(raw, &p)
		total += len(p.Events)
	}
	if total != 2 {
		t.Errorf("total events = %d, want 2", total)
	}
}

func TestReporter_SendsBearerToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := NewReporter(srv.URL, "workspace-abc", "app", "prod")
	r.Send(app.NewMessageEvent(app.EventSuccess, "done"))
	r.Close()

	if gotAuth != "Bearer workspace-abc" {
		t.Errorf("Authorization = %q, want Bearer workspace-abc", gotAuth)
	}
}

func TestReporter_BufferDropsOldest(t *testing.T) {
	// Server that always fails — reporter can't drain.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	r := NewReporter(srv.URL, "", "app", "prod")

	// Fill the buffer beyond capacity. Should not block or panic.
	for i := 0; i < reporterBufSize+100; i++ {
		r.Send(app.NewMessageEvent(app.EventProgress, "event"))
	}

	r.Close()
}

func TestReporter_RetriesOnFailure(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		n := atomic.AddInt32(&attempts, 1)
		if n <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := NewReporter(srv.URL, "", "app", "prod")
	// Send events across multiple flush cycles to trigger retries.
	for i := 0; i < 3; i++ {
		r.Send(app.NewMessageEvent(app.EventSuccess, "event"))
		time.Sleep(reporterFlushEvery + 20*time.Millisecond)
	}
	r.Close()

	got := atomic.LoadInt32(&attempts)
	if got < 2 {
		t.Errorf("expected at least 2 attempts (retry on failure), got %d", got)
	}
}

// ── teeOutput tests ────────────────────────────────────────────────────────

func TestTeeOutput_NilReporter_ReturnsPrimary(t *testing.T) {
	primary := &testutil.MockOutput{}
	out := newTeeOutput(primary, nil)
	if out != primary {
		t.Error("with nil reporter, teeOutput should return the primary directly")
	}
}

func TestTeeOutput_BothReceiveEvents(t *testing.T) {
	var mu sync.Mutex
	var received []app.Event

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var payload struct{ Events []app.Event }
		json.Unmarshal(data, &payload)
		mu.Lock()
		received = append(received, payload.Events...)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	reporter := NewReporter(srv.URL, "", "app", "prod")
	primary := &testutil.MockOutput{}
	out := newTeeOutput(primary, reporter)

	out.Command("server", "create", "master")
	out.Progress("installing docker")
	out.Success("done")
	out.Warning("slow DNS")
	out.Info("info msg")
	out.Error(fmt.Errorf("something failed"))

	reporter.Close()

	// Primary should have received all events.
	if len(primary.Commands) != 1 {
		t.Errorf("primary commands = %d, want 1", len(primary.Commands))
	}
	if len(primary.Successes) != 1 {
		t.Errorf("primary successes = %d, want 1", len(primary.Successes))
	}
	if len(primary.Warnings) != 1 {
		t.Errorf("primary warnings = %d, want 1", len(primary.Warnings))
	}

	// Reporter should have received all events too.
	mu.Lock()
	defer mu.Unlock()
	if len(received) != 6 {
		t.Errorf("reporter events = %d, want 6", len(received))
	}
}

func TestTeeOutput_Writer(t *testing.T) {
	var mu sync.Mutex
	var received []app.Event

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var payload struct{ Events []app.Event }
		json.Unmarshal(data, &payload)
		mu.Lock()
		received = append(received, payload.Events...)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	reporter := NewReporter(srv.URL, "", "app", "prod")
	primary := &testutil.MockOutput{}
	out := newTeeOutput(primary, reporter)

	w := out.Writer()
	w.Write([]byte("build log line"))

	reporter.Close()

	mu.Lock()
	defer mu.Unlock()
	foundStream := false
	for _, ev := range received {
		if ev.Type == app.EventStream {
			foundStream = true
		}
	}
	if !foundStream {
		t.Error("expected stream event from Writer()")
	}
}
