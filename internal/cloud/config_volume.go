package cloud

import (
	"fmt"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/spf13/cobra"
)

func NewVolumeCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "volume", Short: "Manage volumes in config"}
	cmd.AddCommand(newVolumeSetCmd())
	cmd.AddCommand(newVolumeRemoveCmd())
	return cmd
}

func newVolumeSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "set <name>", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			size, _ := cmd.Flags().GetInt("size")
			server, _ := cmd.Flags().GetString("server")

			return mutateConfig(func(cfg *config.AppConfig) error {
				if cfg.Volumes == nil {
					cfg.Volumes = map[string]config.VolumeDef{}
				}
				cfg.Volumes[args[0]] = config.VolumeDef{Size: size, Server: server}
				fmt.Printf("volume %q set\n", args[0])
				return nil
			})
		},
	}
	cmd.Flags().Int("size", 0, "volume size in GB")
	cmd.Flags().String("server", "", "server to attach to")
	return cmd
}

func newVolumeRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use: "remove <name>", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return mutateConfig(func(cfg *config.AppConfig) error {
				delete(cfg.Volumes, args[0])
				fmt.Printf("volume %q removed\n", args[0])
				return nil
			})
		},
	}
}
