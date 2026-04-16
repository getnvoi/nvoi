package main

import (
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/spf13/cobra"
)

func newLogsCmd(rt *runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs <service>",
		Short: "Stream service logs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			follow, _ := cmd.Flags().GetBool("follow")
			tail, _ := cmd.Flags().GetInt("tail")
			since, _ := cmd.Flags().GetString("since")
			previous, _ := cmd.Flags().GetBool("previous")
			timestamps, _ := cmd.Flags().GetBool("timestamps")
			return app.Logs(cmd.Context(), app.LogsRequest{
				Cluster:    rt.dc.Cluster,
				Service:    args[0],
				Follow:     follow,
				Tail:       tail,
				Since:      since,
				Previous:   previous,
				Timestamps: timestamps,
			})
		},
	}
	cmd.Flags().BoolP("follow", "f", false, "follow log output")
	cmd.Flags().IntP("tail", "n", 50, "lines from end")
	cmd.Flags().String("since", "", "show logs since duration (e.g. 5m)")
	cmd.Flags().Bool("previous", false, "previous container logs")
	cmd.Flags().Bool("timestamps", false, "include timestamps")
	return cmd
}
