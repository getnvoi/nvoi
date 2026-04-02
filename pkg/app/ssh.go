package app

import (
	"context"
	"strings"
)

type SSHRequest struct {
	Cluster
	Command []string
}

func SSH(ctx context.Context, req SSHRequest) error {
	ssh, _, err := req.Cluster.SSH(ctx)
	if err != nil {
		return err
	}
	defer ssh.Close()

	out := req.Log()
	cmd := strings.Join(req.Command, " ")
	return ssh.RunStream(ctx, cmd, out.Writer(), out.Writer())
}
