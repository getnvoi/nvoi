package testutil

import "io"

// MockOutput implements app.Output for testing (captures events).
//
// Why this mock stays (while MockCompute / MockDNS / MockBucket were nuked):
// Output is an INTERNAL UI contract, not an external system boundary. The
// alternative — running every test through the real renderer and parsing
// stdout — is strictly worse than capturing structured events directly.
// See internal/testutil/providermocks.go for the governance rules covering
// provider-boundary mocks.
type MockOutput struct {
	Commands   []string
	Progresses []string
	Successes  []string
	Warnings   []string
	Infos      []string
	Errors     []error
}

func (m *MockOutput) Command(command, action, name string, extra ...any) {
	m.Commands = append(m.Commands, command+"/"+action+"/"+name)
}
func (m *MockOutput) Progress(msg string) { m.Progresses = append(m.Progresses, msg) }
func (m *MockOutput) Success(msg string)  { m.Successes = append(m.Successes, msg) }
func (m *MockOutput) Warning(msg string)  { m.Warnings = append(m.Warnings, msg) }
func (m *MockOutput) Info(msg string)     { m.Infos = append(m.Infos, msg) }
func (m *MockOutput) Error(err error)     { m.Errors = append(m.Errors, err) }
func (m *MockOutput) Writer() io.Writer   { return io.Discard }
