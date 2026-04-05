package core

import (
	"encoding/json"
	"fmt"
	"io"
)

// Output is the contract for emitting structured events from pkg/core/ to the viewer.
// pkg/core/ calls these methods. internal/core/ and internal/api/ provide implementations.
// pkg/core/ never imports fmt for output. pkg/core/ never writes to stdout.
type Output interface {
	// Command opens a group — everything after belongs to this command until the next Command or Error.
	Command(command, action, name string, extra ...any)
	Progress(msg string)
	Success(msg string)
	Warning(msg string)
	Info(msg string)
	// Error emits an error event. Does NOT exit — pkg/core/ returns the error, caller handles exit.
	Error(err error)
	// Writer returns an io.Writer for streaming output (e.g. docker build logs).
	// Lines are indented/styled by the renderer.
	Writer() io.Writer
}

// ── JSONL event types ────────────────────────────────────────────────────────
// Shared format for CLI --json output, API deployment logs, and CLI streaming.
// One JSON object per line. "type" discriminates.

const (
	EventCommand  = "command"
	EventProgress = "progress"
	EventSuccess  = "success"
	EventWarning  = "warning"
	EventInfo     = "info"
	EventError    = "error"
	EventStream   = "stream"
)

// Event is a structured output event. Serialized as one JSONL line.
type Event struct {
	Type    string         `json:"type"`
	Message string         `json:"message,omitempty"`
	Command string         `json:"command,omitempty"`
	Action  string         `json:"action,omitempty"`
	Name    string         `json:"name,omitempty"`
	Extra   map[string]any `json:"extra,omitempty"`
}

// MarshalEvent serializes an event to a JSONL line (no trailing newline).
func MarshalEvent(ev Event) string {
	b, _ := json.Marshal(ev)
	return string(b)
}

// ParseEvent deserializes a JSONL line into an Event.
func ParseEvent(line string) (Event, error) {
	var ev Event
	err := json.Unmarshal([]byte(line), &ev)
	return ev, err
}

// NewCommandEvent creates a command event with optional extra key-value pairs.
func NewCommandEvent(command, action, name string, extra ...any) Event {
	ev := Event{Type: EventCommand, Command: command, Action: action, Name: name}
	if len(extra) >= 2 {
		ev.Extra = map[string]any{}
		for i := 0; i+1 < len(extra); i += 2 {
			if k, ok := extra[i].(string); ok {
				ev.Extra[k] = extra[i+1]
			}
		}
	}
	return ev
}

// NewMessageEvent creates an event with just a type and message.
func NewMessageEvent(eventType, message string) Event {
	return Event{Type: eventType, Message: message}
}

// ReplayEvent dispatches an event through an Output implementation.
// Used by the CLI to render JSONL logs through TUI/Plain/JSON renderers.
func ReplayEvent(ev Event, out Output) {
	switch ev.Type {
	case EventCommand:
		extra := make([]any, 0, len(ev.Extra)*2)
		for k, v := range ev.Extra {
			extra = append(extra, k, v)
		}
		out.Command(ev.Command, ev.Action, ev.Name, extra...)
	case EventProgress:
		out.Progress(ev.Message)
	case EventSuccess:
		out.Success(ev.Message)
	case EventWarning:
		out.Warning(ev.Message)
	case EventInfo:
		out.Info(ev.Message)
	case EventError:
		out.Error(fmt.Errorf("%s", ev.Message))
	case EventStream:
		out.Writer().Write([]byte(ev.Message + "\n"))
	}
}
