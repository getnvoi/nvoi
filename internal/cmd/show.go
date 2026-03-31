package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show full deployment status — fetches everything live",
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO Phase 2:
			// 1. Resolve provider
			// 2. List servers by label (provider API) → print table
			// 3. List volumes by label (provider API) → print table
			// 4. If master reachable (SSH):
			//    - kubectl get nodes (on remote) → print
			//    - kubectl get deployments,statefulsets -o wide (on remote) → print
			//    - kubectl get pods -o wide (on remote) → print
			//    - kubectl get services (on remote) → print
			//    - Check Caddy deployment + TLS status
			// 5. List DNS records (DNS API if configured) → print table
			// 6. Print URLs
			return fmt.Errorf("not implemented")
		},
	}
}
