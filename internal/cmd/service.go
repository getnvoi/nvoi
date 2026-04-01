package cmd

import (
	"fmt"

	"github.com/getnvoi/nvoi/internal/app"
	"github.com/spf13/cobra"
)

func newServiceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Manage service definitions",
	}
	cmd.AddCommand(newServiceSetCmd())
	cmd.AddCommand(newServiceDeleteCmd())
	return cmd
}

func newServiceSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set [name]",
		Short: "Deploy a service to the cluster",
		Long: `Creates or updates a k8s workload.

Examples:
  nvoi service set db --provider hetzner --image postgres:17 --volume pgdata:/var/lib/postgresql/data
  nvoi service set web --provider hetzner --image 10.0.1.1:5000/web:20260401 --port 80 --replicas 2
  nvoi service set redis --provider hetzner --image redis:7 --port 6379`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			providerName, _ := cmd.Flags().GetString("provider")
			image, _ := cmd.Flags().GetString("image")
			port, _ := cmd.Flags().GetInt("port")
			replicas, _ := cmd.Flags().GetInt("replicas")
			command, _ := cmd.Flags().GetString("command")
			server, _ := cmd.Flags().GetString("server")
			volumes, _ := cmd.Flags().GetStringArray("volume")
			healthPath, _ := cmd.Flags().GetString("health-path")
			envVars, _ := cmd.Flags().GetStringArray("env")

			appName, env, err := resolveAppEnv()
			if err != nil {
				return err
			}
			creds, err := resolveCredentials(cmd, providerName)
			if err != nil {
				return err
			}
			sshKey, err := resolveSSHKey()
			if err != nil {
				return err
			}

			return app.ServiceSet(cmd.Context(), app.ServiceSetRequest{
				AppName:     appName,
				Env:         env,
				Provider:    providerName,
				Credentials: creds,
				SSHKey:      sshKey,
				Name:        args[0],
				Image:       image,
				Port:        port,
				Command:     command,
				Replicas:    replicas,
				EnvVars:     envVars,
				Volumes:     volumes,
				HealthPath:  healthPath,
				Server:      server,
			})
		},
	}
	addProviderFlags(cmd)
	cmd.Flags().String("image", "", "container image (required)")
	cmd.Flags().Int("port", 0, "container port (0 = no exposed port)")
	cmd.Flags().Int("replicas", 1, "number of replicas")
	cmd.Flags().String("command", "", "override container command")
	cmd.Flags().String("server", "", "target server for node selector")
	cmd.Flags().StringArray("volume", nil, "volume mount (name:/path)")
	cmd.Flags().String("health-path", "", "readiness probe HTTP path")
	cmd.Flags().StringArray("env", nil, "environment variable (KEY=VALUE)")
	_ = cmd.MarkFlagRequired("image")
	return cmd
}

func newServiceDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete [name]",
		Short: "Delete a service from the cluster",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			providerName, _ := cmd.Flags().GetString("provider")
			yes, _ := cmd.Flags().GetBool("yes")

			if !yes {
				fmt.Printf("Delete service %s? [y/N] ", args[0])
				var confirm string
				fmt.Scanln(&confirm)
				if confirm != "y" && confirm != "yes" {
					fmt.Println("aborted.")
					return nil
				}
			}

			appName, env, err := resolveAppEnv()
			if err != nil {
				return err
			}
			creds, err := resolveCredentials(cmd, providerName)
			if err != nil {
				return err
			}
			sshKey, err := resolveSSHKey()
			if err != nil {
				return err
			}

			return app.ServiceDelete(cmd.Context(), app.ServiceDeleteRequest{
				AppName:     appName,
				Env:         env,
				Provider:    providerName,
				Credentials: creds,
				SSHKey:      sshKey,
				Name:        args[0],
			})
		},
	}
	addProviderFlags(cmd)
	cmd.Flags().BoolP("yes", "y", false, "skip confirmation")
	return cmd
}
