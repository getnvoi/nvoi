package core

import (
	"context"
)

// WaitRolloutRequest asks the cluster to wait for a specific service's
// rollout to complete. Scoped to one service — doesn't block on unrelated pods.
type WaitRolloutRequest struct {
	Cluster
	Output         Output
	Service        string // service name (deployment/statefulset name)
	WorkloadKind   string // "deployment" or "statefulset"
	HasHealthCheck bool   // true if the service has a readiness probe
}

// WaitRollout waits for a specific service's rollout to complete.
func WaitRollout(ctx context.Context, req WaitRolloutRequest) error {
	out := log(req.Output)
	names, err := req.Cluster.Names()
	if err != nil {
		return err
	}
	return req.Kube.WaitRolloutReady(ctx, names.KubeNamespace(), req.Service, req.WorkloadKind, req.HasHealthCheck, out)
}
