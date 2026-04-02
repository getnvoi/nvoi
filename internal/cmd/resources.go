package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/getnvoi/nvoi/pkg/app"
	_ "github.com/getnvoi/nvoi/pkg/provider/cloudflare" // register cloudflare DNS
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
				zone, _ := resolveDNSZone(cmd)
				if zone != "" {
					dnsCreds, _ = resolveDNSCredentials(dnsProvider, zone)
				}
			}

			req := app.ResourcesRequest{
				Provider:    providerName,
				Credentials: creds,
				DNSProvider: dnsProvider,
				DNSCreds:    dnsCreds,
			}

			jsonOutput, _ := cmd.Flags().GetBool("json")
			if jsonOutput {
				out, err := app.ResourcesJSON(cmd.Context(), req)
				if err != nil {
					return err
				}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(out)
			}

			res, err := app.Resources(cmd.Context(), req)
			if err != nil {
				return err
			}

			g := NewTableGroup()

			compute := fmt.Sprintf("SERVERS (%s)", providerName)
			t := g.Add(compute, "ID", "NAME", "STATUS", "IPv4", "PRIVATE IP")
			for _, s := range res.Servers {
				t.Row(s.ID, s.Name, s.Status, s.IPv4, s.PrivateIP)
			}

			t = g.Add(fmt.Sprintf("FIREWALLS (%s)", providerName), "ID", "NAME")
			for _, fw := range res.Firewalls {
				t.Row(fw.ID, fw.Name)
			}

			t = g.Add(fmt.Sprintf("NETWORKS (%s)", providerName), "ID", "NAME")
			for _, n := range res.Networks {
				t.Row(n.ID, n.Name)
			}

			if len(res.DNSRecords) > 0 {
				t = g.Add(fmt.Sprintf("DNS RECORDS (%s)", dnsProvider), "TYPE", "DOMAIN", "IP")
				for _, r := range res.DNSRecords {
					t.Row(r.Type, r.Domain, r.IP)
				}
			}

			g.Print()
			providers := []string{providerName}
			if dnsProvider != "" {
				providers = append(providers, dnsProvider)
			}
			fmt.Println(dimStyle.Render(fmt.Sprintf("  retrieved from %s", strings.Join(providers, ", "))))
			fmt.Println(dimStyle.Render(fmt.Sprintf("  generated at %s", time.Now().Format("2006-01-02 15:04:05"))))
			fmt.Println()
			return nil
		},
	}
	addComputeProviderFlags(cmd)
	addDNSProviderFlags(cmd)
	cmd.Flags().String("zone", "", "DNS zone (env: DNS_ZONE)")
	return cmd
}
