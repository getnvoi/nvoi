package core

import (
	"testing"
)

func TestMarshalEvent_Command(t *testing.T) {
	ev := NewCommandEvent("instance", "set", "master", "role", "master")
	line := MarshalEvent(ev)

	parsed, err := ParseEvent(line)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Type != EventCommand {
		t.Errorf("type = %q", parsed.Type)
	}
	if parsed.Command != "instance" {
		t.Errorf("command = %q", parsed.Command)
	}
	if parsed.Action != "set" {
		t.Errorf("action = %q", parsed.Action)
	}
	if parsed.Name != "master" {
		t.Errorf("name = %q", parsed.Name)
	}
	if parsed.Extra["role"] != "master" {
		t.Errorf("extra[role] = %v", parsed.Extra["role"])
	}
}

func TestMarshalEvent_CommandNoExtra(t *testing.T) {
	ev := NewCommandEvent("service", "delete", "web")
	line := MarshalEvent(ev)

	parsed, err := ParseEvent(line)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Extra != nil {
		t.Errorf("extra should be nil, got %v", parsed.Extra)
	}
}

func TestMarshalEvent_Message(t *testing.T) {
	tests := []string{EventProgress, EventSuccess, EventWarning, EventInfo, EventError, EventStream}
	for _, typ := range tests {
		ev := NewMessageEvent(typ, "hello world")
		line := MarshalEvent(ev)

		parsed, err := ParseEvent(line)
		if err != nil {
			t.Fatalf("%s parse: %v", typ, err)
		}
		if parsed.Type != typ {
			t.Errorf("%s: type = %q", typ, parsed.Type)
		}
		if parsed.Message != "hello world" {
			t.Errorf("%s: message = %q", typ, parsed.Message)
		}
	}
}

func TestParseEvent_Invalid(t *testing.T) {
	_, err := ParseEvent("not json")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestMarshalEvent_Roundtrip(t *testing.T) {
	ev := Event{
		Type:    EventCommand,
		Command: "dns",
		Action:  "set",
		Name:    "web",
		Extra:   map[string]any{"domains": []any{"example.com"}},
	}
	line := MarshalEvent(ev)
	parsed, err := ParseEvent(line)
	if err != nil {
		t.Fatalf("roundtrip: %v", err)
	}
	if parsed.Type != ev.Type || parsed.Command != ev.Command || parsed.Action != ev.Action || parsed.Name != ev.Name {
		t.Errorf("roundtrip mismatch: got %+v", parsed)
	}
}
