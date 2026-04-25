// Package sshmock holds the canned-response SSH mock used by every test
// that exercises code consuming utils.SSHClient.
//
// Why this lives in its own sub-package: pkg/infra needs MockSSH at test
// time. internal/testutil's other production file (providermocks.go) imports
// the per-provider concrete clients — including pkg/provider/infra/hetzner,
// which itself depends on pkg/infra. Pulling that whole graph into pkg/infra's
// test compile creates an import cycle. Splitting MockSSH out keeps
// pkg/infra tests free of provider-mock baggage. The parent testutil package
// re-exports the types via aliases so every other consumer is unaffected.
package sshmock

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net"
	"strings"
	"sync"

	"github.com/getnvoi/nvoi/pkg/utils"
)

// MockSSH implements utils.SSHClient with canned command responses.
// Commands are matched by prefix — the first matching entry wins.
type MockSSH struct {
	mu       sync.Mutex
	Commands map[string]MockResult // exact command → result
	Prefixes []MockPrefix          // prefix matches, checked in order
	Uploads  []MockUpload          // recorded uploads
	Calls    []string              // recorded Run commands
	// Stdins holds the payload captured from every RunWithStdin call, keyed
	// by the command string. Used by SSH BuildProvider tests to assert that
	// env + nvoi.yaml were piped correctly in a single invocation.
	Stdins map[string][]byte
	Closed bool
}

// MockResult is a canned response for an SSH command.
type MockResult struct {
	Output []byte
	Err    error
}

// MockPrefix matches commands by prefix.
type MockPrefix struct {
	Prefix string
	Result MockResult
}

// MockUpload records an Upload call.
type MockUpload struct {
	Path    string
	Content []byte
	Mode    fs.FileMode
}

// NewMockSSH creates a MockSSH with the given exact command mappings.
func NewMockSSH(commands map[string]MockResult) *MockSSH {
	return &MockSSH{Commands: commands}
}

func (m *MockSSH) Run(_ context.Context, cmd string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, cmd)

	// Exact match first
	if r, ok := m.Commands[cmd]; ok {
		return r.Output, r.Err
	}
	// Prefix match
	for _, p := range m.Prefixes {
		if strings.HasPrefix(cmd, p.Prefix) || strings.Contains(cmd, p.Prefix) {
			return p.Result.Output, p.Result.Err
		}
	}
	return nil, fmt.Errorf("mock ssh: unmatched command: %s", cmd)
}

func (m *MockSSH) RunStream(_ context.Context, cmd string, stdout, stderr io.Writer) error {
	out, err := m.Run(context.Background(), cmd)
	if err != nil {
		return err
	}
	if stdout != nil && len(out) > 0 {
		stdout.Write(out)
	}
	return nil
}

// RunWithStdin records the stdin payload and otherwise behaves exactly like
// RunStream — the canned command response flows to stdout, match rules are
// the same. Tests assert on m.Stdins[cmd] to verify env/config piping.
func (m *MockSSH) RunWithStdin(_ context.Context, cmd string, stdin io.Reader, stdout, stderr io.Writer) error {
	var payload []byte
	if stdin != nil {
		payload, _ = io.ReadAll(stdin)
	}
	m.mu.Lock()
	if m.Stdins == nil {
		m.Stdins = map[string][]byte{}
	}
	m.Stdins[cmd] = payload
	m.mu.Unlock()

	out, err := m.Run(context.Background(), cmd)
	if err != nil {
		return err
	}
	if stdout != nil && len(out) > 0 {
		stdout.Write(out)
	}
	return nil
}

func (m *MockSSH) Upload(_ context.Context, local io.Reader, remotePath string, mode fs.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, _ := io.ReadAll(local)
	m.Uploads = append(m.Uploads, MockUpload{Path: remotePath, Content: data, Mode: mode})
	return nil
}

func (m *MockSSH) Stat(_ context.Context, remotePath string) (*utils.RemoteFileInfo, error) {
	return &utils.RemoteFileInfo{Path: remotePath}, nil
}

func (m *MockSSH) DialTCP(_ context.Context, remoteAddr string) (net.Conn, error) {
	return nil, fmt.Errorf("mock ssh: DialTCP not implemented")
}

func (m *MockSSH) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Closed = true
	return nil
}

var _ utils.SSHClient = (*MockSSH)(nil)
