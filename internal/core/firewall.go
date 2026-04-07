package core

import (
	"os"
	"strings"

	"github.com/getnvoi/nvoi/internal/render"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/spf13/cobra"
)

func newFirewallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "firewall",
		Short: "Manage firewall rules",
	}
	cmd.AddCommand(newFirewallSetCmd())
	cmd.AddCommand(newFirewallListCmd())
	return cmd
}

func newFirewallSetCmd() *cobra.Command {
	cmd := &cobra.Command{
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
			appName, env, err := resolveAppEnv(cmd)
			if err != nil {
				return err
			}
			computeProvider, err := resolveComputeProvider(cmd)
			if err != nil {
				return err
			}
			computeCreds, err := resolveComputeCredentials(cmd, computeProvider)
			if err != nil {
				return err
			}

			if len(args) == 0 {
				if envVal := os.Getenv("NVOI_FIREWALL"); envVal != "" {
					args = strings.Split(envVal, ";")
				}
			}

			allowed, err := provider.ResolveFirewallArgs(cmd.Context(), args)
			if err != nil {
				return err
			}

			return app.FirewallSet(cmd.Context(), app.FirewallSetRequest{
				Cluster: app.Cluster{
					AppName:     appName,
					Env:         env,
					Provider:    computeProvider,
					Credentials: computeCreds,
					Output:      resolveOutput(cmd),
				},
				AllowedIPs: allowed,
			})
		},
	}
	addComputeProviderFlags(cmd)
	addAppFlags(cmd)
	return cmd
}

func newFirewallListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "Show current firewall rules",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			appName, env, err := resolveAppEnv(cmd)
			if err != nil {
				return err
			}
			computeProvider, err := resolveComputeProvider(cmd)
			if err != nil {
				return err
			}
			computeCreds, err := resolveComputeCredentials(cmd, computeProvider)
			if err != nil {
				return err
			}

			rules, err := app.FirewallList(cmd.Context(), app.FirewallListRequest{
				Cluster: app.Cluster{
					AppName:     appName,
					Env:         env,
					Provider:    computeProvider,
					Credentials: computeCreds,
					Output:      resolveOutput(cmd),
				},
			})
			if err != nil {
				return err
			}

			if len(rules) == 0 {
				t := render.NewTable("PORT", "ALLOWED CIDRs")
				t.Row("*", "base rules only (SSH + internal)")
				t.Print()
				return nil
			}
			t := render.NewTable("PORT", "ALLOWED CIDRs")
			for _, port := range provider.SortedPorts(rules) {
				t.Row(port, strings.Join(rules[port], ", "))
			}
			t.Print()
			return nil
		},
	}
	addComputeProviderFlags(cmd)
	addAppFlags(cmd)
	return cmd
}
