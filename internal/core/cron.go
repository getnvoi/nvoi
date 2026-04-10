package core

import (
	"github.com/getnvoi/nvoi/internal/reconcile"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/spf13/cobra"
)

func NewCronCmd(dc *reconcile.DeployContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cron",
		Short: "Manage cron jobs",
	}
	cmd.AddCommand(newCronRunCmd(dc))
	return cmd
}

func newCronRunCmd(dc *reconcile.DeployContext) *cobra.Command {
	return &cobra.Command{
		Use:   "run <name>",
		Short: "Trigger a cron job immediately",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.CronRun(cmd.Context(), app.CronRunRequest{
				Cluster: dc.Cluster,
				Name:    args[0],
			})
		},
	}
}
