package commands

import "github.com/spf13/cobra"

// NewCronCmd returns the cron command group.
func NewCronCmd(b Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cron",
		Short: "Manage scheduled workloads",
	}
	cmd.AddCommand(newCronSetCmd(b))
	cmd.AddCommand(newCronDeleteCmd(b))
	return cmd
}

func newCronSetCmd(b Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set [name]",
		Short: "Deploy a scheduled workload to the cluster",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			image, _ := cmd.Flags().GetString("image")
			command, _ := cmd.Flags().GetString("command")
			server, _ := cmd.Flags().GetString("server")
			schedule, _ := cmd.Flags().GetString("schedule")
			volumes, _ := cmd.Flags().GetStringArray("volume")
			envVars, _ := cmd.Flags().GetStringArray("env")
			secrets, _ := cmd.Flags().GetStringArray("secret")
			storages, _ := cmd.Flags().GetStringArray("storage")

			return b.CronSet(cmd.Context(), args[0], CronOpts{
				WorkloadOpts: WorkloadOpts{
					Image:   image,
					Command: command,
					Server:  server,
					Env:     envVars,
					Secrets: secrets,
					Storage: storages,
					Volumes: volumes,
				},
				Schedule: schedule,
			})
		},
	}
	cmd.Flags().String("image", "", "container image (required)")
	cmd.Flags().String("command", "", "override container command")
	cmd.Flags().String("server", "", "target server for node selector")
	cmd.Flags().String("schedule", "", "cron schedule (required)")
	cmd.Flags().StringArray("volume", nil, "volume mount (name:/path)")
	cmd.Flags().StringArray("env", nil, "environment variable (KEY=VALUE)")
	cmd.Flags().StringArray("secret", nil, "secret key reference or alias (ENV=SECRET_KEY)")
	cmd.Flags().StringArray("storage", nil, "storage name (injects STORAGE_{NAME}_* env vars from secrets)")
	_ = cmd.MarkFlagRequired("image")
	_ = cmd.MarkFlagRequired("schedule")
	return cmd
}

func newCronDeleteCmd(b Backend) *cobra.Command {
	return &cobra.Command{
		Use:   "delete [name]",
		Short: "Delete a scheduled workload from the cluster",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return b.CronDelete(cmd.Context(), args[0])
		},
	}
}
