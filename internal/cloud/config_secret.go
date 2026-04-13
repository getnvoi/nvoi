package cloud

import (
	"fmt"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/spf13/cobra"
)

func NewSecretCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "secret", Short: "Manage secrets in config"}
	cmd.AddCommand(newSecretAddCmd())
	cmd.AddCommand(newSecretRemoveCmd())
	return cmd
}

func newSecretAddCmd() *cobra.Command {
	return &cobra.Command{
		Use: "add <key>", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return mutateConfig(func(cfg *config.AppConfig) error {
				for _, s := range cfg.Secrets {
					if s == args[0] {
						fmt.Printf("secret %q already in config\n", args[0])
						return nil
					}
				}
				cfg.Secrets = append(cfg.Secrets, args[0])
				fmt.Printf("secret %q added\n", args[0])
				return nil
			})
		},
	}
}

func newSecretRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use: "remove <key>", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return mutateConfig(func(cfg *config.AppConfig) error {
				filtered := cfg.Secrets[:0]
				for _, s := range cfg.Secrets {
					if s != args[0] {
						filtered = append(filtered, s)
					}
				}
				cfg.Secrets = filtered
				fmt.Printf("secret %q removed\n", args[0])
				return nil
			})
		},
	}
}
