package core

import (
	"context"

	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
)

type ExecRequest struct {
	Cluster
	Cfg     provider.ProviderConfigView
	Service string
	Command []string
}

// Exec runs a command in the first pod of a service. Streams stdout+stderr
// to the request's Output writer; no shell quoting, no kubectl wrapper.
func Exec(ctx context.Context, req ExecRequest) error {
	kc, names, cleanup, err := req.Cluster.Kube(ctx, req.Cfg)
	if err != nil {
		return err
	}
	defer cleanup()

	ns := names.KubeNamespace()

	pod, err := kc.FirstPod(ctx, ns, req.Service)
	if err != nil {
		return err
	}

	w := req.Log().Writer()
	return kc.Exec(ctx, kube.ExecRequest{
		Namespace: ns,
		Pod:       pod,
		Command:   req.Command,
		Stdout:    w,
		Stderr:    w,
	})
}
