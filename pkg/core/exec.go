package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/pkg/kube"
)

type ExecRequest struct {
	Cluster
	Service string
	Command []string
}

// TODO: migrate to client-go remotecommand — exec requires SPDY, keeping SSH path for now.
func Exec(ctx context.Context, req ExecRequest) error {
	ssh, names, err := req.Cluster.SSH(ctx)
	if err != nil {
		return err
	}
	defer ssh.Close()

	ns := names.KubeNamespace()

	pod, err := req.Kube.FirstPod(ctx, ns, req.Service)
	if err != nil {
		return err
	}

	cmd := fmt.Sprintf("exec %s -- %s", pod, strings.Join(req.Command, " "))
	w := req.Log().Writer()
	return kube.RunStream(ctx, ssh, ns, cmd, w, w)
}
