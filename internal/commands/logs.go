package commands

import "github.com/spf13/cobra"

// NewLogsCmd returns the logs command.
func NewLogsCmd(b Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs [service]",
		Short: "Show service logs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			follow, _ := cmd.Flags().GetBool("follow")
			tail, _ := cmd.Flags().GetInt("tail")
			since, _ := cmd.Flags().GetString("since")
			previous, _ := cmd.Flags().GetBool("previous")
			timestamps, _ := cmd.Flags().GetBool("timestamps")

			return b.Logs(cmd.Context(), args[0], LogsOpts{
				Follow:     follow,
				Tail:       tail,
				Since:      since,
				Previous:   previous,
				Timestamps: timestamps,
			})
		},
	}
	cmd.Flags().BoolP("follow", "f", false, "follow log output")
	cmd.Flags().IntP("tail", "n", 50, "number of lines to show")
	cmd.Flags().String("since", "", "show logs newer than duration (e.g. 5m, 1h)")
	cmd.Flags().Bool("previous", false, "show logs from previous container instance")
	cmd.Flags().Bool("timestamps", false, "show timestamps on each line")
	return cmd
}
