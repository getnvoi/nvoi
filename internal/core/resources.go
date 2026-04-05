package core

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/internal/render"
	_ "github.com/getnvoi/nvoi/pkg/provider/aws"        // register
	_ "github.com/getnvoi/nvoi/pkg/provider/cloudflare" // register
	_ "github.com/getnvoi/nvoi/pkg/provider/scaleway"  // register
	"github.com/spf13/cobra"
)

func newResourcesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "resources",
		Short: "List all resources under the provider accounts",
		RunE: func(cmd *cobra.Command, args []string) error {
			providerName, err := resolveComputeProvider(cmd)
			if err != nil {
				return err
			}
			creds, err := resolveComputeCredentials(cmd, providerName)
			if err != nil {
				return err
			}

			// DNS is optional
			dnsProvider, _ := resolveDNSProvider(cmd)
			var dnsCreds map[string]string
			if dnsProvider != "" {
				dnsCreds, _ = resolveDNSCredentials(cmd, dnsProvider)
			}

			// Storage is optional
			storageProvider, _ := resolveStorageProvider(cmd)
			var storageCreds map[string]string
			if storageProvider != "" {
				storageCreds, _ = resolveStorageCredentials(cmd, storageProvider)
			}

			req := app.ResourcesRequest{
				Compute: app.ProviderRef{Name: providerName, Creds: creds},
				DNS:     app.ProviderRef{Name: dnsProvider, Creds: dnsCreds},
				Storage: app.ProviderRef{Name: storageProvider, Creds: storageCreds},
			}

			groups, err := app.Resources(cmd.Context(), req)
			if err != nil {
				return err
			}

			jsonOutput, _ := cmd.Flags().GetBool("json")
			if jsonOutput {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(groups)
			}

			g := render.NewTableGroup()
			for _, group := range groups {
				if len(group.Rows) == 0 {
					continue
				}
				t := g.Add(group.Name, group.Columns...)
				for _, row := range group.Rows {
					t.Row(row...)
				}
			}
			g.Print()

			providers := []string{providerName}
			if dnsProvider != "" && dnsProvider != providerName {
				providers = append(providers, dnsProvider)
			}
			if storageProvider != "" && storageProvider != providerName && storageProvider != dnsProvider {
				providers = append(providers, storageProvider)
			}
			fmt.Println(render.DimStyle.Render(fmt.Sprintf("  retrieved from %s", strings.Join(providers, ", "))))
			fmt.Println(render.DimStyle.Render(fmt.Sprintf("  generated at %s", time.Now().Format("2006-01-02 15:04:05"))))
			fmt.Println()
			return nil
		},
	}
	addComputeProviderFlags(cmd)
	addDNSProviderFlags(cmd)
	addStorageProviderFlags(cmd)
	cmd.Flags().String("zone", "", "DNS zone (env: DNS_ZONE)")
	return cmd
}
