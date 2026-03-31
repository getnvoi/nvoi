package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newDestroyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "destroy",
		Short: "Tear down all infrastructure",
		Long: `Destroys everything: deletes k8s resources, detaches volumes, deletes servers,
removes firewall, network, and DNS records.

Volumes are detached but NOT deleted — data is preserved in the cloud.
Queries all live infrastructure by naming convention — nothing read from files.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			yes, _ := cmd.Flags().GetBool("yes")
			_ = yes

			// TODO Phase 4:
			// 1. Confirm unless --yes
			// 2. Resolve provider
			// 3. If master reachable (SSH): kubectl delete all nvoi-labeled resources (on remote)
			// 4. List DNS records (DNS API) → delete each
			// 5. List volumes by label (provider API) → detach each (data preserved)
			// 6. List servers by label (provider API) → delete each
			// 7. Delete firewall + network by name (provider API)
			// 8. Clear .env
			//
			// Errors collected, not short-circuited — best-effort teardown.
			// 404s are success (already gone).
			return fmt.Errorf("not implemented")
		},
	}
	cmd.Flags().Bool("yes", false, "skip confirmation")
	return cmd
}
