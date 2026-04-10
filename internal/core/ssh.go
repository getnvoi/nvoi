package core

import (
	"github.com/getnvoi/nvoi/internal/config"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/spf13/cobra"
)

func NewSSHCmd(dc *config.DeployContext) *cobra.Command {
	return &cobra.Command{
		Use:   "ssh -- <command>",
		Short: "Run command on master node",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.SSH(cmd.Context(), app.SSHRequest{Cluster: dc.Cluster, Command: args})
		},
	}
}
