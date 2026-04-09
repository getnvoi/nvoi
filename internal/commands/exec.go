package commands

import (
	"github.com/getnvoi/nvoi/internal/reconcile"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/spf13/cobra"
)

func NewExecCmd(dc *reconcile.DeployContext) *cobra.Command {
	return &cobra.Command{
		Use:   "exec <service> -- <command>",
		Short: "Run command in service pod",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.Exec(cmd.Context(), app.ExecRequest{
				Cluster: dc.Cluster, Service: args[0], Command: args[1:],
			})
		},
	}
}
