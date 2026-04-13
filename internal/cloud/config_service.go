package cloud

import (
	"fmt"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/spf13/cobra"
)

func NewServiceCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "service", Short: "Manage services in config"}
	cmd.AddCommand(newServiceSetCmd())
	cmd.AddCommand(newServiceRemoveCmd())
	return cmd
}

func newServiceSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:  "set <name>",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			image, _ := cmd.Flags().GetString("image")
			build, _ := cmd.Flags().GetString("build")
			port, _ := cmd.Flags().GetInt("port")
			replicas, _ := cmd.Flags().GetInt("replicas")
			command, _ := cmd.Flags().GetString("command")
			health, _ := cmd.Flags().GetString("health")
			server, _ := cmd.Flags().GetString("server")
			servers, _ := cmd.Flags().GetStringSlice("servers")
			env, _ := cmd.Flags().GetStringSlice("env")
			secrets, _ := cmd.Flags().GetStringSlice("secrets")
			storage, _ := cmd.Flags().GetStringSlice("storage")
			volumes, _ := cmd.Flags().GetStringSlice("volumes")

			return mutateConfig(func(cfg *config.AppConfig) error {
				if cfg.Services == nil {
					cfg.Services = map[string]config.ServiceDef{}
				}
				cfg.Services[args[0]] = config.ServiceDef{
					Image: image, Build: build, Port: port, Replicas: replicas,
					Command: command, Health: health, Server: server, Servers: servers,
					Env: env, Secrets: secrets, Storage: storage, Volumes: volumes,
				}
				fmt.Printf("service %q set\n", args[0])
				return nil
			})
		},
	}
	cmd.Flags().String("image", "", "container image")
	cmd.Flags().String("build", "", "build target name")
	cmd.Flags().Int("port", 0, "container port")
	cmd.Flags().Int("replicas", 0, "number of replicas")
	cmd.Flags().String("command", "", "override command")
	cmd.Flags().String("health", "", "health check path")
	cmd.Flags().String("server", "", "pin to server")
	cmd.Flags().StringSlice("servers", nil, "pin to multiple servers")
	cmd.Flags().StringSlice("env", nil, "environment variables (KEY=VALUE)")
	cmd.Flags().StringSlice("secrets", nil, "secret references")
	cmd.Flags().StringSlice("storage", nil, "storage bucket references")
	cmd.Flags().StringSlice("volumes", nil, "volume mounts (name:/path)")
	return cmd
}

func newServiceRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use: "remove <name>", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return mutateConfig(func(cfg *config.AppConfig) error {
				delete(cfg.Services, args[0])
				delete(cfg.Domains, args[0])
				fmt.Printf("service %q removed\n", args[0])
				return nil
			})
		},
	}
}
