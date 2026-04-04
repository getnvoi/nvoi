package core

import (
	"bufio"
	"fmt"
	"io"
	"os"

	"charm.land/lipgloss/v2"
	app "github.com/getnvoi/nvoi/pkg/core"
)

var (
	tuiCommand  = lipgloss.NewStyle().Bold(true).MarginLeft(2)
	tuiProgress = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).MarginLeft(4)
	tuiSuccess  = lipgloss.NewStyle().MarginLeft(4)
	tuiWarning  = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).MarginLeft(4)
	tuiInfo     = lipgloss.NewStyle().MarginLeft(4)
	tuiError    = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true).MarginLeft(4)
	tuiStream   = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
)

type tuiOutput struct{}

var _ app.Output = tuiOutput{}

func NewTUIOutput() app.Output {
	return tuiOutput{}
}

func (tuiOutput) Command(command, action, name string, extra ...any) {
	msg := fmt.Sprintf("%s %s %s", command, action, name)
	for i := 0; i+1 < len(extra); i += 2 {
		msg += fmt.Sprintf(" (%v: %v)", extra[i], extra[i+1])
	}
	fmt.Println(tuiCommand.Render(msg))
}

func (tuiOutput) Progress(msg string) {
	fmt.Println(tuiProgress.Render(msg))
}

func (tuiOutput) Success(msg string) {
	fmt.Println(tuiSuccess.Render("✓ " + msg))
}

func (tuiOutput) Warning(msg string) {
	fmt.Println(tuiWarning.Render("⚠ " + msg))
}

func (tuiOutput) Info(msg string) {
	fmt.Println(tuiInfo.Render(msg))
}

func (tuiOutput) Error(err error) {
	fmt.Println(tuiError.Render("✗ " + err.Error()))
}

// Writer returns a writer that dims and indents each line.
func (tuiOutput) Writer() io.Writer {
	pr, pw := io.Pipe()
	go func() {
		scanner := bufio.NewScanner(pr)
		for scanner.Scan() {
			fmt.Fprintln(os.Stdout, tuiStream.Render("      "+scanner.Text()))
		}
	}()
	return pw
}
