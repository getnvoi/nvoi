package cloud

import (
	"fmt"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/spf13/cobra"
)

func NewStorageConfigCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "storage", Short: "Manage storage buckets in config"}
	cmd.AddCommand(newStorageSetCmd())
	cmd.AddCommand(newStorageRemoveCmd())
	return cmd
}

func newStorageSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "set <name>", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cors, _ := cmd.Flags().GetBool("cors")
			expireDays, _ := cmd.Flags().GetInt("expire-days")

			return mutateConfig(func(cfg *config.AppConfig) error {
				if cfg.Storage == nil {
					cfg.Storage = map[string]config.StorageDef{}
				}
				cfg.Storage[args[0]] = config.StorageDef{CORS: cors, ExpireDays: expireDays}
				fmt.Printf("storage %q set\n", args[0])
				return nil
			})
		},
	}
	cmd.Flags().Bool("cors", false, "enable CORS")
	cmd.Flags().Int("expire-days", 0, "object expiration in days")
	return cmd
}

func newStorageRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use: "remove <name>", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return mutateConfig(func(cfg *config.AppConfig) error {
				delete(cfg.Storage, args[0])
				fmt.Printf("storage %q removed\n", args[0])
				return nil
			})
		},
	}
}
