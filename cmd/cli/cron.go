package main

import "github.com/spf13/cobra"

func newCronCmd(m *mode) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cron",
		Short: "Manage cron jobs",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "run <name>",
		Short: "Trigger a cron job immediately",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return m.backend.CronRun(cmd.Context(), args[0])
		},
	})
	return cmd
}
