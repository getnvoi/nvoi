package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	pkgcore "github.com/getnvoi/nvoi/pkg/core"
)

func TestJSONOutput_Command(t *testing.T) {
	var buf bytes.Buffer
	out := NewJSONOutput(&buf)
	out.Command("instance", "set", "master", "role", "master")

	var ev pkgcore.Event
	if err := json.Unmarshal(buf.Bytes(), &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ev.Type != pkgcore.EventCommand {
		t.Errorf("type = %q", ev.Type)
	}
	if ev.Command != "instance" || ev.Action != "set" || ev.Name != "master" {
		t.Errorf("command event = %+v", ev)
	}
}

func TestJSONOutput_AllEventTypes(t *testing.T) {
	var buf bytes.Buffer
	out := NewJSONOutput(&buf)

	out.Progress("waiting")
	out.Success("done")
	out.Warning("watch out")
	out.Info("fyi")
	out.Error(fmt.Errorf("boom"))

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 5 {
		t.Fatalf("got %d lines, want 5", len(lines))
	}

	expected := []string{pkgcore.EventProgress, pkgcore.EventSuccess, pkgcore.EventWarning, pkgcore.EventInfo, pkgcore.EventError}
	for i, want := range expected {
		var ev pkgcore.Event
		if err := json.Unmarshal([]byte(lines[i]), &ev); err != nil {
			t.Fatalf("line %d: %v", i, err)
		}
		if ev.Type != want {
			t.Errorf("line %d: type = %q, want %q", i, ev.Type, want)
		}
	}
}

func TestReplayEvent_Roundtrip(t *testing.T) {
	// Emit events through JSON output → parse JSONL → replay through JSON output.
	// The second output should produce the same JSONL.
	var buf1 bytes.Buffer
	out1 := NewJSONOutput(&buf1)
	out1.Command("service", "set", "web")
	out1.Progress("applying")
	out1.Success("done")

	var buf2 bytes.Buffer
	out2 := NewJSONOutput(&buf2)

	for _, line := range strings.Split(strings.TrimSpace(buf1.String()), "\n") {
		ReplayLine(line, out2)
	}

	// Both buffers should have the same events (type + key fields match).
	lines1 := strings.Split(strings.TrimSpace(buf1.String()), "\n")
	lines2 := strings.Split(strings.TrimSpace(buf2.String()), "\n")

	if len(lines1) != len(lines2) {
		t.Fatalf("line count: %d vs %d", len(lines1), len(lines2))
	}

	for i := range lines1 {
		var ev1, ev2 pkgcore.Event
		json.Unmarshal([]byte(lines1[i]), &ev1)
		json.Unmarshal([]byte(lines2[i]), &ev2)
		if ev1.Type != ev2.Type {
			t.Errorf("line %d: type %q vs %q", i, ev1.Type, ev2.Type)
		}
		if ev1.Message != ev2.Message {
			t.Errorf("line %d: message %q vs %q", i, ev1.Message, ev2.Message)
		}
	}
}

func TestReplayLine_InvalidJSON(t *testing.T) {
	var buf bytes.Buffer
	out := NewJSONOutput(&buf)
	ReplayLine("not json", out)
	// Should not panic, should not write anything.
	if buf.Len() != 0 {
		t.Errorf("invalid line should be silently dropped, got: %q", buf.String())
	}
}

func TestResolve_JSON(t *testing.T) {
	out := Resolve(true, false)
	if _, ok := out.(*jsonOutput); !ok {
		t.Errorf("json flag should return jsonOutput, got %T", out)
	}
}

func TestResolve_CI(t *testing.T) {
	out := Resolve(false, true)
	if _, ok := out.(plainOutput); !ok {
		t.Errorf("ci flag should return plainOutput, got %T", out)
	}
}
