// Package testutil provides mock implementations of core interfaces for testing.
package testutil

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
	Closed   bool
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
