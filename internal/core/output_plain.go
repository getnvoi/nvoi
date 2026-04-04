package core

import (
	"fmt"
	"io"
	"os"

	app "github.com/getnvoi/nvoi/pkg/core"
)

type plainOutput struct{}

var _ app.Output = plainOutput{}

func NewPlainOutput() app.Output {
	return plainOutput{}
}

func (plainOutput) Command(command, action, name string, extra ...any) {
	msg := fmt.Sprintf("%s %s %s", command, action, name)
	for i := 0; i+1 < len(extra); i += 2 {
		msg += fmt.Sprintf(" (%v: %v)", extra[i], extra[i+1])
	}
	fmt.Printf("[command]  %s\n", msg)
}

func (plainOutput) Progress(msg string) {
	fmt.Printf("  [progress] %s\n", msg)
}

func (plainOutput) Success(msg string) {
	fmt.Printf("  [success]  %s\n", msg)
}

func (plainOutput) Warning(msg string) {
	fmt.Printf("  [warning]  %s\n", msg)
}

func (plainOutput) Info(msg string) {
	fmt.Printf("  [info]     %s\n", msg)
}

func (plainOutput) Error(err error) {
	fmt.Printf("  [error]    %s\n", err.Error())
}

func (plainOutput) Writer() io.Writer {
	return os.Stdout
}
