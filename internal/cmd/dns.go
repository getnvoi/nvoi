package cmd

import (
	"fmt"

	"github.com/getnvoi/nvoi/pkg/app"
	_ "github.com/getnvoi/nvoi/pkg/provider/cloudflare" // register cloudflare DNS
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
		Short: "Create or update DNS A records pointing to service server",
		Long: `Points domains at the server running the service.

Examples:
  nvoi dns set web myapp.com --zone myapp.com
  nvoi dns set web api.myapp.com www.myapp.com --zone myapp.com`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			service := args[0]
			domains := args[1:]

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
			dnsProvider, err := resolveDNSProvider(cmd)
			if err != nil {
				return err
			}
			dnsCreds, err := resolveDNSCredentials(cmd, dnsProvider)
			if err != nil {
				return err
			}
			sshKey, err := resolveSSHKey()
			if err != nil {
				return err
			}

			return app.DNSSet(cmd.Context(), app.DNSSetRequest{
				Cluster: app.Cluster{
					AppName:     appName,
					Env:         env,
					Provider:    computeProvider,
					Credentials: computeCreds,
					SSHKey:      sshKey,
					Output:      resolveOutput(cmd),
				},
				DNS:     app.ProviderRef{Name: dnsProvider, Creds: dnsCreds},
				Service: service,
				Domains: domains,
			})
		},
	}
	addComputeProviderFlags(cmd)
	addDNSProviderFlags(cmd)
	addAppFlags(cmd)
	cmd.Flags().String("zone", "", "DNS zone (env: DNS_ZONE)")
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
			yes, _ := cmd.Flags().GetBool("yes")

			if !yes {
				fmt.Printf("Delete DNS records for %v? [y/N] ", domains)
				var confirm string
				fmt.Scanln(&confirm)
				if confirm != "y" && confirm != "yes" {
					fmt.Println("aborted.")
					return nil
				}
			}

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
			dnsProvider, err := resolveDNSProvider(cmd)
			if err != nil {
				return err
			}
			dnsCreds, err := resolveDNSCredentials(cmd, dnsProvider)
			if err != nil {
				return err
			}
			sshKey, err := resolveSSHKey()
			if err != nil {
				return err
			}

			return app.DNSDelete(cmd.Context(), app.DNSDeleteRequest{
				Cluster: app.Cluster{
					AppName:     appName,
					Env:         env,
					Provider:    computeProvider,
					Credentials: computeCreds,
					SSHKey:      sshKey,
					Output:      resolveOutput(cmd),
				},
				DNS:     app.ProviderRef{Name: dnsProvider, Creds: dnsCreds},
				Service: service,
				Domains: domains,
			})
		},
	}
	addComputeProviderFlags(cmd)
	addDNSProviderFlags(cmd)
	addAppFlags(cmd)
	cmd.Flags().String("zone", "", "DNS zone (env: DNS_ZONE)")
	cmd.Flags().BoolP("yes", "y", false, "skip confirmation")
	return cmd
}

func newDNSListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List DNS records in zone",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dnsProvider, err := resolveDNSProvider(cmd)
			if err != nil {
				return err
			}
			dnsCreds, err := resolveDNSCredentials(cmd, dnsProvider)
			if err != nil {
				return err
			}

			records, err := app.DNSList(cmd.Context(), app.DNSListRequest{
				DNS:    app.ProviderRef{Name: dnsProvider, Creds: dnsCreds},
				Output: resolveOutput(cmd),
			})
			if err != nil {
				return err
			}

			if len(records) == 0 {
				fmt.Println("no records")
				return nil
			}

			for _, r := range records {
				fmt.Printf("%-4s %-40s %s\n", r.Type, r.Domain, r.IP)
			}
			return nil
		},
	}
	addDNSProviderFlags(cmd)
	addAppFlags(cmd)
	cmd.Flags().String("zone", "", "DNS zone (env: DNS_ZONE)")
	return cmd
}
