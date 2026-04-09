package commands

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// NewServiceCmd returns the service command group.
func NewServiceCmd(b Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Manage service definitions",
	}
	cmd.AddCommand(newServiceSetCmd(b))
	cmd.AddCommand(newServiceDeleteCmd(b))
	return cmd
}

func newServiceSetCmd(b Backend) *cobra.Command {
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
			noWait, _ := cmd.Flags().GetBool("no-wait")

			// --secret is a reference, not a setter. KEY only, no aliasing.
			for _, s := range secrets {
				if strings.Contains(s, "=") {
					return fmt.Errorf("--secret %q: use the secret key name directly, not KEY=VALUE", s)
				}
			}

			return b.ServiceSet(cmd.Context(), args[0], ServiceOpts{
				WorkloadOpts: WorkloadOpts{
					Image:   image,
					Command: command,
					Server:  server,
					Env:     envVars,
					Secrets: secrets,
					Storage: storages,
					Volumes: volumes,
				},
				Port:     port,
				Replicas: replicas,
				Health:   healthPath,
				NoWait:   noWait,
			})
		},
	}
	cmd.Flags().String("image", "", "container image (required)")
	_ = cmd.MarkFlagRequired("image")
	cmd.Flags().Int("port", 0, "container port (0 = no exposed port)")
	cmd.Flags().Int("replicas", 1, "number of replicas")
	cmd.Flags().String("command", "", "override container command")
	cmd.Flags().String("server", "", "target server for node selector")
	cmd.Flags().StringArray("volume", nil, "volume mount (name:/path)")
	cmd.Flags().String("health-path", "", "readiness probe HTTP path")
	cmd.Flags().StringArray("env", nil, "environment variable (KEY=VALUE)")
	cmd.Flags().StringArray("secret", nil, "secret key reference (must exist via secret set)")
	cmd.Flags().StringArray("storage", nil, "storage name (injects STORAGE_{NAME}_* env vars from secrets)")
	cmd.Flags().Bool("no-wait", false, "skip waiting for all pods to be ready")
	return cmd
}

func newServiceDeleteCmd(b Backend) *cobra.Command {
	return &cobra.Command{
		Use:   "delete [name]",
		Short: "Delete a service from the cluster",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return b.ServiceDelete(cmd.Context(), args[0])
		},
	}
}
