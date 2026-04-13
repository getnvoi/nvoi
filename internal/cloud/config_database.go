package cloud

import (
	"fmt"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/spf13/cobra"
)

func NewDatabaseConfigCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "database", Short: "Manage databases in config"}
	cmd.AddCommand(newDatabaseSetCmd())
	cmd.AddCommand(newDatabaseRemoveCmd())
	return cmd
}

func newDatabaseSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "set <name>", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind, _ := cmd.Flags().GetString("kind")
			image, _ := cmd.Flags().GetString("image")
			volume, _ := cmd.Flags().GetString("volume")

			return mutateConfig(func(cfg *config.AppConfig) error {
				if cfg.Database == nil {
					cfg.Database = map[string]config.DatabaseDef{}
				}
				cfg.Database[args[0]] = config.DatabaseDef{
					Kind: kind, Image: image, Volume: volume,
				}
				fmt.Printf("database %q set\n", args[0])
				return nil
			})
		},
	}
	cmd.Flags().String("kind", "", "database engine (postgres or mysql)")
	cmd.Flags().String("image", "", "container image (e.g. postgres:17)")
	cmd.Flags().String("volume", "", "volume name for data")
	return cmd
}

func newDatabaseRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use: "remove <name>", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return mutateConfig(func(cfg *config.AppConfig) error {
				delete(cfg.Database, args[0])
				fmt.Printf("database %q removed\n", args[0])
				return nil
			})
		},
	}
}
