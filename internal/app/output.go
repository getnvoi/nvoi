package app

// Output is the contract for emitting structured events from app/ to the viewer.
// app/ calls these methods. cmd/ provides the implementation (TUI or JSONL).
// app/ never imports fmt for output. app/ never writes to stdout.
type Output interface {
	// Command opens a group — everything after belongs to this command until the next Command or Error.
	Command(command, action, name string, extra ...any)
	Progress(msg string)
	Success(msg string)
	Warning(msg string)
	Info(msg string)
	// Error emits an error event. Does NOT exit — app/ returns the error, cobra handles exit.
	Error(err error)
}
