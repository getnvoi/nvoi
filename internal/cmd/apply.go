package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newApplyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "apply",
		Short: "Reconcile the cluster — rebuild and redeploy all services",
		Long: `Queries the cluster for current state, rebuilds services that have
a build field, generates k8s YAML, applies everything. Deploys Caddy
ingress if DNS records exist. Injects secrets if they exist.

One command. Full reconciliation. Idempotent.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO Phase 2:
			// 1. Resolve master from provider API → SSH
			// 2. Query cluster: kubectl get deployments,statefulsets,services (on remote)
			// 3. For services with build field + stale/missing image: run builder
			// 4. Generate k8s YAML from current workloads:
			//    - No env rewriting — k8s namespace handles isolation, service names stay short
			//    - Stateful (managed volume) → StatefulSet, replicas=1, node selector
			//    - Stateless → Deployment
			// 5. If DNS records exist (query DNS API): generate Caddy Deployment + ConfigMap
			// 6. If secrets exist (kubectl get secret on remote): inject envFrom
			// 7. kubectl apply (on remote)
			// 8. Reconcile orphaned resources (kubectl on remote)
			// 9. Wait rollout (kubectl on remote)
			// 10. If Caddy deployed: check TLS (SSH) + probe HTTPS (HTTP)
			return fmt.Errorf("not implemented")
		},
	}
}
