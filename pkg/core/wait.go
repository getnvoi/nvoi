package core

import (
	"context"
	"fmt"
	"time"

	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// WaitAllServicesRequest asks the cluster to poll all pods in the namespace
// until every pod is ready or the timeout expires.
type WaitAllServicesRequest struct {
	Cluster
	Timeout time.Duration // 0 = default 5 minutes
}

// WaitAllServices polls all pods in the namespace until all are ready.
// Reports per-service status via Output. Tolerates transient crashes —
// only fails on timeout (not all pods ready within the window).
func WaitAllServices(ctx context.Context, req WaitAllServicesRequest) error {
	out := req.Log()
	ssh, names, err := req.Cluster.SSH(ctx)
	if err != nil {
		return err
	}
	defer ssh.Close()

	ns := names.KubeNamespace()
	timeout := req.Timeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}

	out.Progress("waiting for all services")

	lastStatus := ""
	return utils.Poll(ctx, 3*time.Second, timeout, func() (bool, error) {
		pods, err := kube.GetAllPods(ctx, ssh, ns)
		if err != nil {
			return false, nil // transient
		}

		if len(pods) == 0 {
			return false, nil
		}

		ready := 0
		total := len(pods)
		var notReady []string

		for _, pod := range pods {
			if pod.Ready {
				ready++
			} else {
				notReady = append(notReady, fmt.Sprintf("%s (%s)", pod.Name, pod.Status))
			}
		}

		status := fmt.Sprintf("%d/%d pods ready", ready, total)
		if status != lastStatus {
			out.Progress(status)
			lastStatus = status
		}

		if ready == total {
			out.Success("all services ready")
			return true, nil
		}

		return false, nil
	})
}
