package core

import (
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/internal/render"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/spf13/cobra"
)

func newCronCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cron",
		Short: "Manage scheduled workloads",
	}
	cmd.AddCommand(newCronSetCmd())
	cmd.AddCommand(newCronDeleteCmd())
	return cmd
}

func newCronSetCmd() *cobra.Command {
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

			for _, s := range secrets {
				if strings.Contains(s, "=") && !strings.Contains(s, "==") {
					parts := strings.SplitN(s, "=", 2)
					if parts[0] == parts[1] {
						continue
					}
				}
			}

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

			cluster := app.Cluster{
				AppName:     appName,
				Env:         env,
				Provider:    providerName,
				Credentials: creds,
				SSHKey:      sshKey,
				Output:      resolveOutput(cmd),
			}

			return app.CronSet(cmd.Context(), app.CronSetRequest{
				Cluster:  cluster,
				Name:     args[0],
				Image:    image,
				Command:  command,
				EnvVars:  envVars,
				Secrets:  secrets,
				Storages: storages,
				Volumes:  volumes,
				Schedule: schedule,
				Server:   server,
			})
		},
	}
	addComputeProviderFlags(cmd)
	addAppFlags(cmd)
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

func newCronDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete [name]",
		Short: "Delete a scheduled workload from the cluster",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			yes, _ := cmd.Flags().GetBool("yes")
			if !yes {
				fmt.Printf("Delete cron %s? [y/N] ", args[0])
				var confirm string
				fmt.Scanln(&confirm)
				if confirm != "y" && confirm != "yes" {
					fmt.Println("aborted.")
					return nil
				}
			}

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

			err = app.CronDelete(cmd.Context(), app.CronDeleteRequest{
				Cluster: app.Cluster{
					AppName:     appName,
					Env:         env,
					Provider:    providerName,
					Credentials: creds,
					SSHKey:      sshKey,
					Output:      resolveOutput(cmd),
				},
				Name: args[0],
			})
			return render.HandleDeleteResult(err, resolveOutput(cmd))
		},
	}
	addComputeProviderFlags(cmd)
	addAppFlags(cmd)
	cmd.Flags().BoolP("yes", "y", false, "skip confirmation")
	return cmd
}
