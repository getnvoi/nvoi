package handlers

import (
	"bufio"
	"io"

	"github.com/danielgtaylor/huma/v2"
	pkgcore "github.com/getnvoi/nvoi/pkg/core"
)

// streamOperation returns a huma.StreamResponse that executes fn with a JSONL output writer.
// Errors from fn are written as the final JSONL error event.
func streamOperation(fn func(out pkgcore.Output) error) *huma.StreamResponse {
	return &huma.StreamResponse{
		Body: func(ctx huma.Context) {
			ctx.SetHeader("Content-Type", "application/x-ndjson")
			w := ctx.BodyWriter()
			out := &jsonlOutput{w: w}

			if err := fn(out); err != nil {
				line := pkgcore.MarshalEvent(pkgcore.NewMessageEvent(pkgcore.EventError, err.Error()))
				w.Write([]byte(line + "\n"))
			}
		},
	}
}

// jsonlOutput implements pkg/core.Output, writing JSONL lines to a writer.
type jsonlOutput struct {
	w io.Writer
}

var _ pkgcore.Output = (*jsonlOutput)(nil)

func (o *jsonlOutput) emit(ev pkgcore.Event) {
	line := pkgcore.MarshalEvent(ev)
	o.w.Write([]byte(line + "\n"))
}

func (o *jsonlOutput) Command(command, action, name string, extra ...any) {
	o.emit(pkgcore.NewCommandEvent(command, action, name, extra...))
}

func (o *jsonlOutput) Progress(msg string) {
	o.emit(pkgcore.NewMessageEvent(pkgcore.EventProgress, msg))
}

func (o *jsonlOutput) Success(msg string) {
	o.emit(pkgcore.NewMessageEvent(pkgcore.EventSuccess, msg))
}

func (o *jsonlOutput) Warning(msg string) {
	o.emit(pkgcore.NewMessageEvent(pkgcore.EventWarning, msg))
}

func (o *jsonlOutput) Info(msg string) {
	o.emit(pkgcore.NewMessageEvent(pkgcore.EventInfo, msg))
}

func (o *jsonlOutput) Error(err error) {
	o.emit(pkgcore.NewMessageEvent(pkgcore.EventError, err.Error()))
}

func (o *jsonlOutput) Writer() io.Writer {
	pr, pw := io.Pipe()
	go func() {
		scanner := bufio.NewScanner(pr)
		for scanner.Scan() {
			o.emit(pkgcore.NewMessageEvent(pkgcore.EventStream, scanner.Text()))
		}
	}()
	return pw
}
