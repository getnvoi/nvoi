package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// waitPollInterval is the polling interval for WaitAllServices. Variable for testing.
var waitPollInterval = 10 * time.Second

// waitCrashTimeout is how long to wait when a pod is in CrashLoopBackOff. Variable for testing.
var waitCrashTimeout = 2 * time.Minute

// WaitAllServicesRequest asks the cluster to poll all pods in the namespace
// until every pod is ready or the timeout expires.
type WaitAllServicesRequest struct {
	Cluster
	Timeout time.Duration // 0 = default 5 minutes
}

// WaitAllServices polls all pods in the namespace until all are ready.
// Reports per-service status via Output. Exits early after waitCrashTimeout
// if any pod is stuck in CrashLoopBackOff, fetching its logs on the final attempt.
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

	var crashStart time.Time // when we first saw CrashLoopBackOff

	return utils.Poll(ctx, waitPollInterval, timeout, func() (bool, error) {
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
		var crashPods []string

		for _, pod := range pods {
			if pod.Ready {
				ready++
			} else {
				notReady = append(notReady, fmt.Sprintf("%s (%s)", pod.Name, pod.Status))
				if pod.Status == "CrashLoopBackOff" {
					crashPods = append(crashPods, pod.Name)
				}
			}
		}

		if ready == total {
			out.Success("all services ready")
			return true, nil
		}

		status := fmt.Sprintf("%d/%d pods ready — waiting: %s", ready, total, strings.Join(notReady, ", "))
		out.Progress(status)

		// Track CrashLoopBackOff duration
		if len(crashPods) > 0 {
			if crashStart.IsZero() {
				crashStart = time.Now()
			} else if time.Since(crashStart) >= waitCrashTimeout {
				// Fetch logs from crashing pods before bailing
				for _, pod := range crashPods {
					logs := kube.RecentLogs(ctx, ssh, ns, pod, "", 30)
					if logs != "" {
						out.Warning(fmt.Sprintf("logs from %s:\n%s", pod, logs))
					}
				}
				return false, fmt.Errorf("pod(s) stuck in CrashLoopBackOff: %s", strings.Join(crashPods, ", "))
			}
		} else {
			crashStart = time.Time{} // reset if no crashes this poll
		}

		return false, nil
	})
}
