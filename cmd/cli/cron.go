package main

import (
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/spf13/cobra"
)

func newCronCmd(rt *runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cron",
		Short: "Manage cron jobs",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "run <name>",
		Short: "Trigger a cron job immediately",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.CronRun(cmd.Context(), app.CronRunRequest{
				Cluster: rt.dc.Cluster,
				Name:    args[0],
			})
		},
	})
	return cmd
}
