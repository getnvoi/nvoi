package cloud

import (
	"fmt"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/spf13/cobra"
)

func NewServerCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "server", Short: "Manage servers in config"}
	cmd.AddCommand(newServerSetCmd())
	cmd.AddCommand(newServerRemoveCmd())
	return cmd
}

func newServerSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "set <name>", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			typ, _ := cmd.Flags().GetString("type")
			region, _ := cmd.Flags().GetString("region")
			role, _ := cmd.Flags().GetString("role")
			disk, _ := cmd.Flags().GetInt("disk")

			return mutateConfig(func(cfg *config.AppConfig) error {
				if cfg.Servers == nil {
					cfg.Servers = map[string]config.ServerDef{}
				}
				cfg.Servers[args[0]] = config.ServerDef{
					Type: typ, Region: region, Role: role, Disk: disk,
				}
				fmt.Printf("server %q set\n", args[0])
				return nil
			})
		},
	}
	cmd.Flags().String("type", "", "server type (e.g. cax11)")
	cmd.Flags().String("region", "", "region (e.g. nbg1)")
	cmd.Flags().String("role", "", "role (master or worker)")
	cmd.Flags().Int("disk", 0, "root disk GB (AWS/Scaleway only)")
	return cmd
}

func newServerRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use: "remove <name>", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return mutateConfig(func(cfg *config.AppConfig) error {
				delete(cfg.Servers, args[0])
				fmt.Printf("server %q removed\n", args[0])
				return nil
			})
		},
	}
}
