package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newDNSCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dns",
		Short: "Manage DNS records",
	}
	cmd.AddCommand(newDNSSetCmd())
	cmd.AddCommand(newDNSDeleteCmd())
	cmd.AddCommand(newDNSListCmd())
	return cmd
}

func newDNSSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set [service] [domain...]",
		Short: "Create or update DNS A records pointing to master",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			service := args[0]
			domains := args[1:]
			providerName, _ := cmd.Flags().GetString("provider")
			zone, _ := cmd.Flags().GetString("zone")

			_ = service
			_ = domains
			_ = providerName
			_ = zone

			// TODO Phase 3:
			// 1. Resolve compute provider → get master server by name → get IP (provider API)
			// 2. Resolve DNS provider from --provider flag
			// 3. For each domain: EnsureARecord(domain, masterIP) — DNS API
			return fmt.Errorf("not implemented")
		},
	}
	cmd.Flags().String("provider", "", "DNS provider (cloudflare, hetzner)")
	cmd.Flags().String("zone", "", "DNS zone (e.g. nvoi.to)")
	_ = cmd.MarkFlagRequired("provider")
	_ = cmd.MarkFlagRequired("zone")
	return cmd
}

func newDNSDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete [service] [domain...]",
		Short: "Delete DNS records",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			service := args[0]
			domains := args[1:]
			providerName, _ := cmd.Flags().GetString("provider")
			zone, _ := cmd.Flags().GetString("zone")

			_ = service
			_ = domains
			_ = providerName
			_ = zone

			// TODO Phase 4:
			// 1. Resolve DNS provider from --provider/--zone flags
			// 2. Delete A records (DNS API)
			return fmt.Errorf("not implemented")
		},
	}
	cmd.Flags().String("provider", "", "DNS provider (cloudflare, hetzner)")
	cmd.Flags().String("zone", "", "DNS zone (e.g. nvoi.to)")
	_ = cmd.MarkFlagRequired("provider")
	_ = cmd.MarkFlagRequired("zone")
	return cmd
}

func newDNSListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List DNS records",
		RunE: func(cmd *cobra.Command, args []string) error {
			providerName, _ := cmd.Flags().GetString("provider")
			zone, _ := cmd.Flags().GetString("zone")

			_ = providerName
			_ = zone

			// TODO Phase 3:
			// 1. Resolve DNS provider from --provider/--zone flags
			// 2. Query DNS API: list A records in zone
			// 3. Print table (domain, type, IP)
			return fmt.Errorf("not implemented")
		},
	}
	cmd.Flags().String("provider", "", "DNS provider (cloudflare, hetzner)")
	cmd.Flags().String("zone", "", "DNS zone (e.g. nvoi.to)")
	_ = cmd.MarkFlagRequired("provider")
	_ = cmd.MarkFlagRequired("zone")
	return cmd
}
