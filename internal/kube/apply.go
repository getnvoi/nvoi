package kube

// TODO Phase 2: Port from nvoi-platform/internal/deploy/kube.go (~87 lines)
//
//   - KubectlApply(ctx, sshClient, yaml) → error
//   - KubectlDelete(ctx, sshClient, yaml) → error
//   - KubectlDeleteByName(ctx, sshClient, kubeName) → error
//   - ReconcileResources(ctx, sshClient, desiredResources) → error
//
// All commands execute via SSH on the master node.
// YAML is base64-encoded, uploaded, then applied.
