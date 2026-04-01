package cmd

import (
	"fmt"
	"os"

	"github.com/getnvoi/nvoi/internal/app"
	_ "github.com/getnvoi/nvoi/internal/provider/cloudflare" // register cloudflare DNS
	"github.com/spf13/cobra"
)

func newResourcesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "resources",
		Short: "List all resources under the provider account",
		RunE: func(cmd *cobra.Command, args []string) error {
			providerName, err := resolveComputeProvider(cmd)
			if err != nil {
				return err
			}
			creds, err := resolveComputeCredentials(cmd, providerName)
			if err != nil {
				return err
			}

			// DNS is optional — don't fail if not configured
			dnsProvider, _ := resolveDNSProvider(cmd)
			var dnsCreds map[string]string
			if dnsProvider != "" {
				zone, _ := cmd.Flags().GetString("zone")
				dnsCreds, _ = resolveDNSCredentials(dnsProvider, zone)
			}

			sshKey, _ := resolveSSHKey()

			res, err := app.Resources(cmd.Context(), app.ResourcesRequest{
				Provider:    providerName,
				Credentials: creds,
				DNSProvider: dnsProvider,
				DNSCreds:    dnsCreds,
				SSHKey:      sshKey,
			})
			if err != nil {
				return err
			}

			fmt.Println("SERVERS")
			t := NewTable("ID", "NAME", "STATUS", "IPv4", "PRIVATE IP")
			for _, s := range res.Servers {
				t.Row(s.ID, s.Name, s.Status, s.IPv4, s.PrivateIP)
			}
			t.Print()

			fmt.Println("\nFIREWALLS")
			t = NewTable("ID", "NAME")
			for _, fw := range res.Firewalls {
				t.Row(fw.ID, fw.Name)
			}
			t.Print()

			fmt.Println("\nNETWORKS")
			t = NewTable("ID", "NAME")
			for _, n := range res.Networks {
				t.Row(n.ID, n.Name)
			}
			t.Print()

			if len(res.DNSRecords) > 0 {
				fmt.Println("\nDNS RECORDS")
				t = NewTable("TYPE", "DOMAIN", "IP")
				for _, r := range res.DNSRecords {
					t.Row(r.Type, r.Domain, r.IP)
				}
				t.Print()
			}

			if len(res.K8sNodes) > 0 {
				fmt.Println("\nK8S NODES")
				t = NewTable("NAME", "STATUS", "ROLES")
				for _, n := range res.K8sNodes {
					t.Row(n.Name, n.Status, n.Roles)
				}
				t.Print()
			}

			if len(res.K8sPods) > 0 {
				fmt.Println("\nK8S PODS")
				t = NewTable("NAMESPACE", "NAME", "STATUS", "NODE")
				for _, p := range res.K8sPods {
					t.Row(p.Namespace, p.Name, p.Status, p.Node)
				}
				t.Print()
			}

			if len(res.K8sServices) > 0 {
				fmt.Println("\nK8S SERVICES")
				t = NewTable("NAMESPACE", "NAME", "TYPE", "CLUSTER-IP", "PORTS")
				for _, s := range res.K8sServices {
					t.Row(s.Namespace, s.Name, s.Type, s.ClusterIP, s.Ports)
				}
				t.Print()
			}

			return nil
		},
	}
	addComputeProviderFlags(cmd)
	addDNSProviderFlags(cmd)
	cmd.Flags().String("zone", "", "DNS zone for listing records")

	// Silence usage on error since DNS/zone are optional
	cmd.SilenceUsage = true
	_ = os.Stdout
	return cmd
}
