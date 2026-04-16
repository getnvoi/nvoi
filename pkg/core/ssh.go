package core

import (
	"context"
	"io"
	"strings"
)

// RunStreamOnMaster executes a shell command on the master, streaming output.
// Bootstrap: wraps real SSH RunStream. Agent: wraps exec.Command with pipes.
type RunStreamOnMaster func(ctx context.Context, cmd string, stdout, stderr io.Writer) error

type SSHRequest struct {
	Cluster
	Output          Output
	Command         []string
	RunStreamMaster RunStreamOnMaster
}

func SSH(ctx context.Context, req SSHRequest) error {
	out := log(req.Output)
	cmd := strings.Join(req.Command, " ")
	return req.RunStreamMaster(ctx, cmd, out.Writer(), out.Writer())
}
