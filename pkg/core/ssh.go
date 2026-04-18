package core

import (
	"context"
	"strings"

	"github.com/getnvoi/nvoi/pkg/provider"
)

type SSHRequest struct {
	Cluster
	Cfg     provider.ProviderConfigView
	Command []string
}

func SSH(ctx context.Context, req SSHRequest) error {
	ssh, _, err := req.Cluster.SSH(ctx, req.Cfg)
	if err != nil {
		return err
	}
	defer ssh.Close()

	out := req.Log()
	cmd := strings.Join(req.Command, " ")
	return ssh.RunStream(ctx, cmd, out.Writer(), out.Writer())
}
