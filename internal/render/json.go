package render

import (
	"bufio"
	"encoding/json"
	"io"

	pkgcore "github.com/getnvoi/nvoi/pkg/core"
)

type jsonOutput struct {
	enc *json.Encoder
}

var _ pkgcore.Output = (*jsonOutput)(nil)

func NewJSONOutput(w io.Writer) pkgcore.Output {
	return &jsonOutput{enc: json.NewEncoder(w)}
}

func (j *jsonOutput) Command(command, action, name string, extra ...any) {
	j.enc.Encode(pkgcore.NewCommandEvent(command, action, name, extra...))
}

func (j *jsonOutput) Progress(msg string) {
	j.enc.Encode(pkgcore.NewMessageEvent(pkgcore.EventProgress, msg))
}

func (j *jsonOutput) Success(msg string) {
	j.enc.Encode(pkgcore.NewMessageEvent(pkgcore.EventSuccess, msg))
}

func (j *jsonOutput) Warning(msg string) {
	j.enc.Encode(pkgcore.NewMessageEvent(pkgcore.EventWarning, msg))
}

func (j *jsonOutput) Info(msg string) {
	j.enc.Encode(pkgcore.NewMessageEvent(pkgcore.EventInfo, msg))
}

func (j *jsonOutput) Error(err error) {
	j.enc.Encode(pkgcore.NewMessageEvent(pkgcore.EventError, err.Error()))
}

func (j *jsonOutput) Writer() io.Writer {
	pr, pw := io.Pipe()
	go func() {
		scanner := bufio.NewScanner(pr)
		for scanner.Scan() {
			j.enc.Encode(pkgcore.NewMessageEvent(pkgcore.EventStream, scanner.Text()))
		}
	}()
	return pw
}
