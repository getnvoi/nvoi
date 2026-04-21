package utils

import (
	"context"
	"io"
	"io/fs"
	"net"
)

// SSHClient abstracts an SSH connection to a remote server.
type SSHClient interface {
	Run(ctx context.Context, cmd string) ([]byte, error)
	RunStream(ctx context.Context, cmd string, stdout, stderr io.Writer) error
	// RunWithStdin runs cmd on the remote host, feeding stdin from the
	// provided reader and streaming stdout/stderr to the provided writers.
	// The SSH BuildProvider (pkg/provider/build/ssh) uses this to pipe
	// the push-side registry password into `docker login --password-stdin`
	// on a builder server — the password never hits argv / ps / shell
	// history. stdin is drained before the command exits; callers close
	// or EOF the reader to signal end-of-input.
	RunWithStdin(ctx context.Context, cmd string, stdin io.Reader, stdout, stderr io.Writer) error
	Upload(ctx context.Context, local io.Reader, remotePath string, mode fs.FileMode) error
	Stat(ctx context.Context, remotePath string) (*RemoteFileInfo, error)
	DialTCP(ctx context.Context, remoteAddr string) (net.Conn, error)
	Close() error
}

type RemoteFileInfo struct {
	Path  string
	Size  int64
	Mode  fs.FileMode
	IsDir bool
}
