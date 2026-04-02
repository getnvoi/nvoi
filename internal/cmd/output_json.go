package cmd

import (
	"bufio"
	"encoding/json"
	"io"

	"github.com/getnvoi/nvoi/pkg/app"
)

type jsonOutput struct {
	enc *json.Encoder
}

var _ app.Output = (*jsonOutput)(nil)

func NewJSONOutput(w io.Writer) app.Output {
	return &jsonOutput{enc: json.NewEncoder(w)}
}

func (j *jsonOutput) Command(command, action, name string, extra ...any) {
	ev := map[string]any{
		"type":    "command",
		"command": command,
		"action":  action,
		"name":    name,
	}
	for i := 0; i+1 < len(extra); i += 2 {
		if k, ok := extra[i].(string); ok {
			ev[k] = extra[i+1]
		}
	}
	j.enc.Encode(ev)
}

func (j *jsonOutput) Progress(msg string) {
	j.enc.Encode(map[string]string{"type": "progress", "message": msg})
}

func (j *jsonOutput) Success(msg string) {
	j.enc.Encode(map[string]string{"type": "success", "message": msg})
}

func (j *jsonOutput) Warning(msg string) {
	j.enc.Encode(map[string]string{"type": "warning", "message": msg})
}

func (j *jsonOutput) Info(msg string) {
	j.enc.Encode(map[string]string{"type": "info", "message": msg})
}

func (j *jsonOutput) Error(err error) {
	j.enc.Encode(map[string]string{"type": "error", "message": err.Error()})
}

// Writer returns a writer that emits each line as a stream event.
func (j *jsonOutput) Writer() io.Writer {
	pr, pw := io.Pipe()
	go func() {
		scanner := bufio.NewScanner(pr)
		for scanner.Scan() {
			j.enc.Encode(map[string]string{"type": "stream", "message": scanner.Text()})
		}
	}()
	return pw
}
