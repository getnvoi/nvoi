package cmd

import (
	"fmt"

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
		Short: "Define or update a service",
		Long: `Creates or updates a k8s workload. Writes directly to the cluster.

Examples:
  nvoi service set web --build myorg/myapp --port 3000 --replicas 2 --health-path /up
  nvoi service set db --image postgres:17 --volume pgdata:/var/lib/postgresql/data
  nvoi service set redis --image redis:7 --port 6379
  nvoi service set jobs --build myorg/myapp --command "bundle exec sidekiq"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			image, _ := cmd.Flags().GetString("image")
			build, _ := cmd.Flags().GetString("build")
			branch, _ := cmd.Flags().GetString("branch")
			port, _ := cmd.Flags().GetInt("port")
			replicas, _ := cmd.Flags().GetInt("replicas")
			command, _ := cmd.Flags().GetString("command")
			server, _ := cmd.Flags().GetString("server")
			volumes, _ := cmd.Flags().GetStringArray("volume")
			healthPath, _ := cmd.Flags().GetString("health-path")
			envVars, _ := cmd.Flags().GetStringArray("env")

			_ = name
			_ = image
			_ = build
			_ = branch
			_ = port
			_ = replicas
			_ = command
			_ = server
			_ = volumes
			_ = healthPath
			_ = envVars

			// TODO Phase 2:
			// 1. Resolve master from provider API → SSH
			// 2. Generate k8s Deployment/StatefulSet + Service from flags
			// 3. kubectl apply (on remote)
			// 4. Wait rollout
			return fmt.Errorf("not implemented")
		},
	}
	cmd.Flags().String("image", "", "stock container image (e.g. postgres:17)")
	cmd.Flags().String("build", "", "git repo to build (e.g. myorg/myapp)")
	cmd.Flags().String("branch", "", "git branch to build (default: main)")
	cmd.Flags().Int("port", 0, "container port (0 = no exposed port)")
	cmd.Flags().Int("replicas", 1, "number of replicas")
	cmd.Flags().String("command", "", "override container command")
	cmd.Flags().String("server", "", "target server (default: master, for multi-node)")
	cmd.Flags().StringArray("volume", nil, "volume mount (name:/path)")
	cmd.Flags().String("health-path", "", "readiness probe HTTP path")
	cmd.Flags().StringArray("env", nil, "environment variable (KEY=VALUE)")
	return cmd
}

func newServiceDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete [name]",
		Short: "Delete a service from the cluster",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO Phase 2:
			// 1. Resolve master from provider API → SSH
			// 2. kubectl delete deployment/statefulset + service by name (on remote)
			return fmt.Errorf("not implemented")
		},
	}
}
