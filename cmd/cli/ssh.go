package main

import (
	"github.com/getnvoi/nvoi/internal/config"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/spf13/cobra"
)

// newSSHCmd dispatches `nvoi ssh -- <cmd>` to the host node. The
// on-demand SSH resolution lives inside Cluster.SSH (route through
// infra.NodeShell when NodeShell field is nil); providers without a
// host shell return an actionable error from there.
func newSSHCmd(rt *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "ssh -- <command>",
		Short: "Run command on host node",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.SSH(cmd.Context(), app.SSHRequest{
				Cluster: rt.dc.Cluster,
				Cfg:     config.NewView(rt.cfg),
				Command: args,
			})
		},
	}
}
