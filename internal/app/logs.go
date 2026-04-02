package app

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/internal/kube"
)

type LogsRequest struct {
	Cluster
	Service    string
	Follow     bool
	Tail       int
	Since      string // duration: "5m", "1h", "24h"
	Previous   bool
	Timestamps bool
}

func Logs(ctx context.Context, req LogsRequest) error {
	ssh, names, err := req.Cluster.SSH(ctx)
	if err != nil {
		return err
	}
	defer ssh.Close()

	ns := names.KubeNamespace()
	sel := kube.PodSelector(req.Service)

	args := fmt.Sprintf(" -l %s", sel)
	if req.Follow {
		args += " -f"
	}
	if req.Tail > 0 {
		args += fmt.Sprintf(" --tail=%d", req.Tail)
	}
	if req.Since != "" {
		args += fmt.Sprintf(" --since=%s", req.Since)
	}
	if req.Previous {
		args += " --previous"
	}
	if req.Timestamps {
		args += " --timestamps"
	}

	w := req.Log().Writer()
	return kube.RunStream(ctx, ssh, ns, "logs"+args, w, w)
}
