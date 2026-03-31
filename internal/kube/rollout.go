package kube

// TODO Phase 2: Port from nvoi-platform/internal/deploy/rollout.go (~86 lines)
//
//   - WaitRollout(ctx, sshClient, workloads) → error
//     Runs: kubectl rollout status deployment/{name} --timeout=300s (on remote via SSH)
//     Then: kubectl get pods -o json (on remote) — parse structured output.
//     On failure: captures recent logs for debugging.
