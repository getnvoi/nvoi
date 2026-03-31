package kube

// TODO Phase 2: Port from nvoi-platform/internal/manifest/generate.go (~331 lines)
//
// Generate takes service definitions and produces k8s YAML:
//   - Stateless service (no volumes) → Deployment + Service
//   - Stateful service (managed volume) → StatefulSet (replicas=1) + Service
//   - Port > 0 → ClusterIP Service; Port == 0 → headless Service
//   - Env vars: no rewriting — k8s namespace handles isolation
//   - Readiness probe if port > 0 and has domains
//
// Pure function — takes service definitions + naming, returns YAML string.
// No I/O, no SSH, no provider calls.
