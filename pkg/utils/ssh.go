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
