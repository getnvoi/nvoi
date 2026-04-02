package app

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

func Exec(ctx context.Context, req ExecRequest) error {
	ssh, names, err := req.Cluster.SSH(ctx)
	if err != nil {
		return err
	}
	defer ssh.Close()

	ns := names.KubeNamespace()

	pod, err := kube.FirstPod(ctx, ssh, ns, req.Service)
	if err != nil {
		return err
	}

	cmd := fmt.Sprintf("exec %s -- %s", pod, strings.Join(req.Command, " "))
	w := req.Log().Writer()
	return kube.RunStream(ctx, ssh, ns, cmd, w, w)
}
