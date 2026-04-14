package cloud

import (
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/spf13/cobra"
)

func NewFirewallCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "firewall", Short: "Manage firewall in config"}
	cmd.AddCommand(newFirewallSetCmd())
	return cmd
}

func newFirewallSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <rules>",
		Short: "Set firewall rules (\"default\" or comma-separated port:cidr)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return mutateConfig(func(cfg *config.AppConfig) error {
				if args[0] == "default" {
					cfg.Firewall = []string{"default"}
				} else {
					cfg.Firewall = strings.Split(args[0], ",")
				}
				fmt.Printf("firewall set to %q\n", args[0])
				return nil
			})
		},
	}
}
