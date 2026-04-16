package core

import (
	"context"
)

type ExecRequest struct {
	Cluster
	Output  Output
	Service string
	Command []string
}

func Exec(ctx context.Context, req ExecRequest) error {
	names, err := req.Cluster.Names()
	if err != nil {
		return err
	}
	ns := names.KubeNamespace()

	pod, err := req.Kube.FirstPod(ctx, ns, req.Service)
	if err != nil {
		return err
	}

	w := log(req.Output).Writer()
	return req.Kube.ExecInPod(ctx, ns, pod, req.Command, w, w)
}
