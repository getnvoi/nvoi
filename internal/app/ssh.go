package app

import (
	"context"
	"os"
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

	cmd := strings.Join(req.Command, " ")
	return ssh.RunStream(ctx, cmd, os.Stdout, os.Stderr)
}
