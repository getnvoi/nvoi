package kube

// TODO Phase 3: Port from nvoi-platform/internal/manifest/caddy.go
//
// GenerateCaddyfile produces a Caddyfile from DNS routes:
//   domain1 {
//       reverse_proxy nvoi-{workspace}-{service}:{port}
//   }
//
// GenerateCaddyDeployment produces the Caddy k8s Deployment + ConfigMap.
// Caddy runs with hostNetwork: true on master node.
