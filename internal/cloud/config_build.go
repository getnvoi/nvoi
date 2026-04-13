package cloud

import (
	"fmt"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/spf13/cobra"
)

func NewBuildConfigCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "build", Short: "Manage build targets in config"}
	cmd.AddCommand(newBuildSetCmd())
	cmd.AddCommand(newBuildRemoveCmd())
	return cmd
}

func newBuildSetCmd() *cobra.Command {
	return &cobra.Command{
		Use: "set <name> <path>", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return mutateConfig(func(cfg *config.AppConfig) error {
				if cfg.Build == nil {
					cfg.Build = map[string]string{}
				}
				cfg.Build[args[0]] = args[1]
				fmt.Printf("build %q set → %s\n", args[0], args[1])
				return nil
			})
		},
	}
}

func newBuildRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use: "remove <name>", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return mutateConfig(func(cfg *config.AppConfig) error {
				delete(cfg.Build, args[0])
				fmt.Printf("build %q removed\n", args[0])
				return nil
			})
		},
	}
}
