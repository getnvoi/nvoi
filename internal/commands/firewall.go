package commands

import "github.com/spf13/cobra"

// NewFirewallCmd returns the firewall command group.
func NewFirewallCmd(b Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "firewall",
		Short: "Manage firewall rules",
	}
	cmd.AddCommand(newFirewallSetCmd(b))
	cmd.AddCommand(newFirewallListCmd(b))
	return cmd
}

func newFirewallSetCmd(b Backend) *cobra.Command {
	return &cobra.Command{
		Use:   "set [preset] [port:cidr,cidr ...]",
		Short: "Set allowed IPs for public ports (omitted ports are closed)",
		Long: `Set firewall rules for public-facing ports. Internal ports are always preserved.

Presets: default, cloudflare
Raw rules: port:cidr,cidr (e.g. 80:0.0.0.0/0 443:10.0.0.0/8)
Mix: preset + raw overrides (raw wins for same port)

Examples:
  nvoi firewall set default                        # 80/443 open to all
  nvoi firewall set cloudflare                      # 80/443 restricted to Cloudflare IPs
  nvoi firewall set 80:0.0.0.0/0 443:0.0.0.0/0     # explicit rules
  nvoi firewall set cloudflare 443:0.0.0.0/0        # 80 from CF preset, 443 overridden
  nvoi firewall set                                 # base rules only (close HTTP)`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return b.FirewallSet(cmd.Context(), args)
		},
	}
}

func newFirewallListCmd(b Backend) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Show current firewall rules",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return b.FirewallList(cmd.Context())
		},
	}
}
