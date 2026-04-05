package render

import (
	"fmt"

	pkgcore "github.com/getnvoi/nvoi/pkg/core"
)

// ReplayEvent dispatches a parsed JSONL event through an Output implementation.
// Used by the CLI to render API deployment logs through TUI/Plain/JSON renderers.
func ReplayEvent(ev pkgcore.Event, out pkgcore.Output) {
	switch ev.Type {
	case pkgcore.EventCommand:
		extra := make([]any, 0, len(ev.Extra)*2)
		for k, v := range ev.Extra {
			extra = append(extra, k, v)
		}
		out.Command(ev.Command, ev.Action, ev.Name, extra...)
	case pkgcore.EventProgress:
		out.Progress(ev.Message)
	case pkgcore.EventSuccess:
		out.Success(ev.Message)
	case pkgcore.EventWarning:
		out.Warning(ev.Message)
	case pkgcore.EventInfo:
		out.Info(ev.Message)
	case pkgcore.EventError:
		out.Error(fmt.Errorf("%s", ev.Message))
	case pkgcore.EventStream:
		out.Writer().Write([]byte(ev.Message + "\n"))
	}
}

// ReplayLine parses a JSONL line and replays it through the output.
// Invalid lines are silently dropped.
func ReplayLine(line string, out pkgcore.Output) {
	ev, err := pkgcore.ParseEvent(line)
	if err != nil {
		return
	}
	ReplayEvent(ev, out)
}
