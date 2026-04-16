package core

import (
	"context"
	"strings"
)

type SSHRequest struct {
	Cluster
	Output  Output
	Command []string
}

func SSH(ctx context.Context, req SSHRequest) error {
	ssh, _, err := req.Cluster.SSH(ctx)
	if err != nil {
		return err
	}
	defer ssh.Close()

	out := log(req.Output)
	cmd := strings.Join(req.Command, " ")
	return ssh.RunStream(ctx, cmd, out.Writer(), out.Writer())
}
