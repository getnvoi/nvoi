package core

import (
	"bufio"
	"encoding/json"
	"io"

	app "github.com/getnvoi/nvoi/pkg/core"
)

type jsonOutput struct {
	enc *json.Encoder
}

var _ app.Output = (*jsonOutput)(nil)

func NewJSONOutput(w io.Writer) app.Output {
	return &jsonOutput{enc: json.NewEncoder(w)}
}

func (j *jsonOutput) Command(command, action, name string, extra ...any) {
	j.enc.Encode(app.NewCommandEvent(command, action, name, extra...))
}

func (j *jsonOutput) Progress(msg string) {
	j.enc.Encode(app.NewMessageEvent(app.EventProgress, msg))
}

func (j *jsonOutput) Success(msg string) {
	j.enc.Encode(app.NewMessageEvent(app.EventSuccess, msg))
}

func (j *jsonOutput) Warning(msg string) {
	j.enc.Encode(app.NewMessageEvent(app.EventWarning, msg))
}

func (j *jsonOutput) Info(msg string) {
	j.enc.Encode(app.NewMessageEvent(app.EventInfo, msg))
}

func (j *jsonOutput) Error(err error) {
	j.enc.Encode(app.NewMessageEvent(app.EventError, err.Error()))
}

// Writer returns a writer that emits each line as a stream event.
func (j *jsonOutput) Writer() io.Writer {
	pr, pw := io.Pipe()
	go func() {
		scanner := bufio.NewScanner(pr)
		for scanner.Scan() {
			j.enc.Encode(app.NewMessageEvent(app.EventStream, scanner.Text()))
		}
	}()
	return pw
}
