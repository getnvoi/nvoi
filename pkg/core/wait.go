package core

import "context"

// WaitRolloutRequest asks the cluster to wait for a specific service's
// rollout to complete. Scoped to one service — doesn't block on unrelated pods.
type WaitRolloutRequest struct {
	Cluster
	Service        string // service name (deployment/statefulset name)
	WorkloadKind   string // "deployment" or "statefulset"
	HasHealthCheck bool   // true if the service has a readiness probe
}

// WaitRollout waits for a specific service's rollout to complete.
// Polls pods by label, detects terminal failures, and verifies stability for
// services without health checks.
func WaitRollout(ctx context.Context, req WaitRolloutRequest) error {
	out := req.Log()
	kc, names, cleanup, err := req.Cluster.Kube(ctx)
	if err != nil {
		return err
	}
	defer cleanup()

	ns := names.KubeNamespace()
	return kc.WaitRollout(ctx, ns, req.Service, req.WorkloadKind, req.HasHealthCheck, out)
}
