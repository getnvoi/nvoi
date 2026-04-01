package kube

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/getnvoi/nvoi/internal/core"
)

// WaitRollout waits for a workload to reach Ready.
// kind is "deployment" or "statefulset".
func WaitRollout(ctx context.Context, ssh core.SSHClient, ns, name, kind string) error {
	cmd := kubectl(ns, fmt.Sprintf("rollout status %s/%s --timeout=300s", kind, name))
	out, err := ssh.Run(ctx, cmd)
	if err != nil {
		logs := recentLogs(ctx, ssh, ns, name, kind)
		if logs != "" {
			return fmt.Errorf("%s/%s rollout failed: %s\nlogs: %s", kind, name, string(out), logs)
		}
		return fmt.Errorf("%s/%s rollout failed: %s", kind, name, string(out))
	}
	return nil
}

// WaitPods polls until all pods in the namespace are Running or Completed.
func WaitPods(ctx context.Context, ssh core.SSHClient, ns string) error {
	return core.Poll(ctx, 3*time.Second, 2*time.Minute, func() (bool, error) {
		out, err := ssh.Run(ctx, kubectl(ns, "get pods --no-headers"))
		if err != nil {
			return false, nil
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line == "" {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 3 {
				continue
			}
			status := fields[2]
			if status != "Running" && status != "Completed" {
				return false, nil
			}
		}
		return true, nil
	})
}

func recentLogs(ctx context.Context, ssh core.SSHClient, ns, name, kind string) string {
	out, err := ssh.Run(ctx, kubectl(ns, fmt.Sprintf("logs %s/%s --tail=20", kind, name)))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
