package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newBootstrapCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "bootstrap",
		Short: "Set up the cluster on provisioned servers",
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO Phase 2:
			// 1. Resolve master server from provider API: nvoi-{workspace}-master → get IP
			// 2. SSH to master
			// 3. Discover private interface (ip addr on remote)
			// 4. Install k3s server on master (idempotent — checks kubectl get nodes)
			//    - Disable traefik/servicelb (we use Caddy)
			//    - Flannel vxlan on private interface
			// 5. Wait for cluster ready (kubectl get nodes on remote)
			// 6. Start registry on master (docker run on remote)
			// 7. List all servers by label from provider API — join non-master as workers:
			//    - Read k3s token from master (cat on remote)
			//    - SSH to worker, install k3s worker (idempotent — checks systemctl)
			//    - Wait for node Ready (kubectl on master)
			// 8. Label all nodes (kubectl on master)
			// 9. Configure all nodes for insecure registry (SSH to each)
			//
			// Idempotent: re-run after adding workers to join them.
			// All checks hit real infrastructure — provider API + SSH + kubectl.
			return fmt.Errorf("not implemented")
		},
	}
}
