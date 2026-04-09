package core

import (
	"fmt"

	"github.com/getnvoi/nvoi/internal/render"
	app "github.com/getnvoi/nvoi/pkg/core"
	_ "github.com/getnvoi/nvoi/pkg/provider/aws"        // register aws DNS
	_ "github.com/getnvoi/nvoi/pkg/provider/cloudflare" // register cloudflare DNS
	_ "github.com/getnvoi/nvoi/pkg/provider/scaleway"   // register scaleway DNS
	"github.com/spf13/cobra"
)

func newDNSCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dns",
		Short: "Manage DNS records only",
	}
	cmd.AddCommand(newDNSSetCmd())
	cmd.AddCommand(newDNSDeleteCmd())
	cmd.AddCommand(newDNSListCmd())
	return cmd
}

func newDNSSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set service:domain,domain [...]",
		Short: "Create or update DNS A records pointing to the service server",
		Long: `Points domains at the server running the service.

Examples:
  nvoi dns set web:myapp.com
  nvoi dns set web:myapp.com,www.myapp.com
  nvoi dns set web:myapp.com api:api.myapp.com
  nvoi dns set web:myapp.com --cloudflare-managed`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			routes, err := app.ParseIngressArgs(args)
			if err != nil {
				return err
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
			cloudflareManaged, _ := cmd.Flags().GetBool("cloudflare-managed")

			for _, route := range routes {
				if err := app.DNSSet(cmd.Context(), app.DNSSetRequest{
					Cluster: app.Cluster{
						AppName:     appName,
						Env:         env,
						Provider:    computeProvider,
						Credentials: computeCreds,
						SSHKey:      sshKey,
						Output:      resolveOutput(cmd),
					},
					DNS:         app.ProviderRef{Name: dnsProvider, Creds: dnsCreds},
					Service:     route.Service,
					Domains:     route.Domains,
					EdgeProxied: cloudflareManaged,
				}); err != nil {
					return err
				}
			}
			return nil
		},
	}
	addComputeProviderFlags(cmd)
	addDNSProviderFlags(cmd)
	addAppFlags(cmd)
	cmd.Flags().String("zone", "", "DNS zone (env: DNS_ZONE)")
	cmd.Flags().Bool("cloudflare-managed", false, "enable Cloudflare-managed DNS proxying")
	return cmd
}

func newDNSDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete service:domain,domain [...]",
		Short: "Delete DNS records (fails if ingress still uses them)",
		Long: `Deletes DNS A records for the specified service:domain pairs.

Examples:
  nvoi dns delete web:myapp.com -y
  nvoi dns delete web:myapp.com,www.myapp.com -y
  nvoi dns delete web:myapp.com api:api.myapp.com -y`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			routes, err := app.ParseIngressArgs(args)
			if err != nil {
				return err
			}
			yes, _ := cmd.Flags().GetBool("yes")

			if !yes {
				fmt.Printf("Delete DNS records for %s? [y/N] ", args)
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

			for _, route := range routes {
				err = app.DNSDelete(cmd.Context(), app.DNSDeleteRequest{
					Cluster: app.Cluster{
						AppName:     appName,
						Env:         env,
						Provider:    computeProvider,
						Credentials: computeCreds,
						SSHKey:      sshKey,
						Output:      resolveOutput(cmd),
					},
					DNS:     app.ProviderRef{Name: dnsProvider, Creds: dnsCreds},
					Service: route.Service,
					Domains: route.Domains,
				})
				if rerr := render.HandleDeleteResult(err, resolveOutput(cmd)); rerr != nil {
					return rerr
				}
			}
			return nil
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
