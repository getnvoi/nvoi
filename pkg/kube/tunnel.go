package kube

// Well-known tunnel agent workload names. Used by:
//   - pkg/provider/cloudflare/workloads.go and pkg/provider/ngrok/workloads.go
//     when emitting the agent Deployment / Secret / ConfigMap (must stay in sync).
//   - pkg/core/describe.go to label tunnel-token Secrets in describe output.
//
// Sweep is now owner-scoped (kc.SweepOwned with utils.OwnerTunnel) so we
// no longer need to enumerate these names for cleanup. The constants
// remain because the providers still need stable Deployment/Secret/CM names.
const (
	CloudflareTunnelAgentName  = "cloudflared"
	CloudflareTunnelSecretName = "cloudflared-token"
	NgrokTunnelAgentName       = "ngrok"
	NgrokTunnelSecretName      = "ngrok-authtoken"
	NgrokTunnelConfigMapName   = "ngrok-config"
)
