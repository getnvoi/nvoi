package cloud

import (
	"fmt"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/spf13/cobra"
)

func NewCronConfigCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "cron", Short: "Manage cron jobs in config"}
	cmd.AddCommand(newCronSetCmd())
	cmd.AddCommand(newCronRemoveCmd())
	return cmd
}

func newCronSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "set <name>", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			image, _ := cmd.Flags().GetString("image")
			build, _ := cmd.Flags().GetString("build")
			schedule, _ := cmd.Flags().GetString("schedule")
			command, _ := cmd.Flags().GetString("command")
			server, _ := cmd.Flags().GetString("server")
			servers, _ := cmd.Flags().GetStringSlice("servers")
			env, _ := cmd.Flags().GetStringSlice("env")
			secrets, _ := cmd.Flags().GetStringSlice("secrets")
			storage, _ := cmd.Flags().GetStringSlice("storage")
			volumes, _ := cmd.Flags().GetStringSlice("volumes")

			return mutateConfig(func(cfg *config.AppConfig) error {
				if cfg.Crons == nil {
					cfg.Crons = map[string]config.CronDef{}
				}
				cfg.Crons[args[0]] = config.CronDef{
					Image: image, Build: build, Schedule: schedule, Command: command,
					Server: server, Servers: servers, Env: env, Secrets: secrets,
					Storage: storage, Volumes: volumes,
				}
				fmt.Printf("cron %q set\n", args[0])
				return nil
			})
		},
	}
	cmd.Flags().String("image", "", "container image")
	cmd.Flags().String("build", "", "build target name")
	cmd.Flags().String("schedule", "", "cron schedule expression")
	cmd.Flags().String("command", "", "override command")
	cmd.Flags().String("server", "", "pin to server")
	cmd.Flags().StringSlice("servers", nil, "pin to multiple servers")
	cmd.Flags().StringSlice("env", nil, "environment variables (KEY=VALUE)")
	cmd.Flags().StringSlice("secrets", nil, "secret references")
	cmd.Flags().StringSlice("storage", nil, "storage bucket references")
	cmd.Flags().StringSlice("volumes", nil, "volume mounts (name:/path)")
	return cmd
}

func newCronRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use: "remove <name>", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return mutateConfig(func(cfg *config.AppConfig) error {
				delete(cfg.Crons, args[0])
				fmt.Printf("cron %q removed\n", args[0])
				return nil
			})
		},
	}
}
