package cmd

import (
	"github.com/getnvoi/nvoi/internal/app"
	"github.com/spf13/cobra"
)

func newLogsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs [service]",
		Short: "Show service logs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appName, env, err := resolveAppEnv(cmd)
			if err != nil {
				return err
			}
			providerName, err := resolveComputeProvider(cmd)
			if err != nil {
				return err
			}
			creds, err := resolveComputeCredentials(cmd, providerName)
			if err != nil {
				return err
			}
			sshKey, err := resolveSSHKey()
			if err != nil {
				return err
			}

			follow, _ := cmd.Flags().GetBool("follow")
			tail, _ := cmd.Flags().GetInt("tail")
			since, _ := cmd.Flags().GetString("since")
			previous, _ := cmd.Flags().GetBool("previous")
			timestamps, _ := cmd.Flags().GetBool("timestamps")

			return app.Logs(cmd.Context(), app.LogsRequest{
				Cluster: app.Cluster{
					AppName:     appName,
					Env:         env,
					Provider:    providerName,
					Credentials: creds,
					SSHKey:      sshKey,
					Output:      resolveOutput(cmd),
				},
				Service:    args[0],
				Follow:     follow,
				Tail:       tail,
				Since:      since,
				Previous:   previous,
				Timestamps: timestamps,
			})
		},
	}
	addComputeProviderFlags(cmd)
	addAppFlags(cmd)
	cmd.Flags().BoolP("follow", "f", false, "follow log output")
	cmd.Flags().IntP("tail", "n", 50, "number of lines to show")
	cmd.Flags().String("since", "", "show logs newer than duration (e.g. 5m, 1h)")
	cmd.Flags().Bool("previous", false, "show logs from previous container instance")
	cmd.Flags().Bool("timestamps", false, "show timestamps on each line")
	return cmd
}
