package core

import (
	"context"

	"github.com/getnvoi/nvoi/pkg/kube"
)

// WaitRolloutRequest asks the cluster to wait for a specific service's
// rollout to complete. Scoped to one service — doesn't block on unrelated pods.
type WaitRolloutRequest struct {
	Cluster
	Service        string // service name (deployment/statefulset name)
	WorkloadKind   string // "deployment" or "statefulset"
	HasHealthCheck bool   // true if the service has a readiness probe
}

// WaitRollout waits for a specific service's rollout to complete.
func WaitRollout(ctx context.Context, req WaitRolloutRequest) error {
	out := req.Log()
	names, err := req.Cluster.Names()
	if err != nil {
		return err
	}
	ns := names.KubeNamespace()

	if req.Kube != nil {
		return req.Kube.WaitRolloutReady(ctx, ns, req.Service, req.WorkloadKind, req.HasHealthCheck, out)
	}

	// Fallback: SSH kubectl path (bootstrap).
	ssh, _, err := req.Cluster.SSH(ctx)
	if err != nil {
		return err
	}
	defer ssh.Close()
	return kube.WaitRollout(ctx, ssh, ns, req.Service, req.WorkloadKind, req.HasHealthCheck, out)
}
