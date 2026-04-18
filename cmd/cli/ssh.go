package main

import (
	"fmt"

	"github.com/getnvoi/nvoi/internal/config"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/spf13/cobra"
)

// newSSHCmd dispatches `nvoi ssh -- <cmd>` to the host node. Goes
// through InfraProvider.NodeShell so providers without a host shell
// (managed k8s, sandbox runtimes that don't expose SSH) fail fast with
// an actionable error instead of falling into the legacy
// Cluster.SSH() on-demand path which would error opaquely.
func newSSHCmd(rt *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "ssh -- <command>",
		Short: "Run command on host node",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			bctx := config.BootstrapContext(rt.dc, rt.cfg)
			infra, err := provider.ResolveInfra(bctx.ProviderName, rt.dc.Cluster.Credentials)
			if err != nil {
				return fmt.Errorf("resolve infra provider: %w", err)
			}
			defer func() { _ = infra.Close() }()

			shell, err := infra.NodeShell(cmd.Context(), bctx)
			if err != nil {
				return fmt.Errorf("infra.NodeShell: %w", err)
			}
			if shell == nil {
				return fmt.Errorf(
					"nvoi ssh: infra provider %q has no node shell — "+
						"sandbox / managed-k8s providers don't expose host SSH. "+
						"Use `nvoi exec <service>` for in-pod shell instead.",
					bctx.ProviderName)
			}
			rt.dc.Cluster.NodeShell = shell

			return app.SSH(cmd.Context(), app.SSHRequest{
				Cluster: rt.dc.Cluster,
				Command: args,
			})
		},
	}
}
