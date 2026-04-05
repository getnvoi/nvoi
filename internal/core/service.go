package core

import (
	"fmt"
	"strings"

	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/internal/render"
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
  nvoi service set db --image postgres:17 --volume pgdata:/var/lib/postgresql/data
  nvoi service set web --image 10.0.1.1:5000/web:20260401 --port 80 --replicas 2
  nvoi service set redis --image redis:7 --port 6379`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			image, _ := cmd.Flags().GetString("image")
			port, _ := cmd.Flags().GetInt("port")
			replicas, _ := cmd.Flags().GetInt("replicas")
			command, _ := cmd.Flags().GetString("command")
			server, _ := cmd.Flags().GetString("server")
			volumes, _ := cmd.Flags().GetStringArray("volume")
			healthPath, _ := cmd.Flags().GetString("health-path")
			envVars, _ := cmd.Flags().GetStringArray("env")
			secrets, _ := cmd.Flags().GetStringArray("secret")
			storages, _ := cmd.Flags().GetStringArray("storage")

			// --secret is a reference, not a setter. KEY only, no KEY=VALUE.
			for _, s := range secrets {
				if strings.Contains(s, "=") {
					return fmt.Errorf("--secret %q must be a key name only, not KEY=VALUE.\n  Store the value first: nvoi secret set %s <value>\n  Then reference it:     --secret %s", s, strings.SplitN(s, "=", 2)[0], strings.SplitN(s, "=", 2)[0])
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

			return app.ServiceSet(cmd.Context(), app.ServiceSetRequest{
				Cluster: app.Cluster{
					AppName:     appName,
					Env:         env,
					Provider:    providerName,
					Credentials: creds,
					SSHKey:      sshKey,
					Output:      resolveOutput(cmd),
				},
				Name:       args[0],
				Image:      image,
				Port:       port,
				Command:    command,
				Replicas:   replicas,
				EnvVars:    envVars,
				Secrets:    secrets,
				Storages:   storages,
				Volumes:    volumes,
				HealthPath: healthPath,
				Server:     server,
			})
		},
	}
	addComputeProviderFlags(cmd)
	addAppFlags(cmd)
	cmd.Flags().String("image", "", "container image (required)")
	cmd.Flags().Int("port", 0, "container port (0 = no exposed port)")
	cmd.Flags().Int("replicas", 1, "number of replicas")
	cmd.Flags().String("command", "", "override container command")
	cmd.Flags().String("server", "", "target server for node selector")
	cmd.Flags().StringArray("volume", nil, "volume mount (name:/path)")
	cmd.Flags().String("health-path", "", "readiness probe HTTP path")
	cmd.Flags().StringArray("env", nil, "environment variable (KEY=VALUE)")
	cmd.Flags().StringArray("secret", nil, "secret key reference (must exist via secret set)")
	cmd.Flags().StringArray("storage", nil, "storage name (injects STORAGE_{NAME}_* env vars from secrets)")
	_ = cmd.MarkFlagRequired("image")
	return cmd
}

func newServiceDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete [name]",
		Short: "Delete a service from the cluster",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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

			err = app.ServiceDelete(cmd.Context(), app.ServiceDeleteRequest{
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
