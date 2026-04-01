package cmd

import (
	"fmt"

	"github.com/getnvoi/nvoi/internal/app"
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

			res, err := app.Resources(cmd.Context(), app.ResourcesRequest{
				Provider:    providerName,
				Credentials: creds,
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

			return nil
		},
	}
	addComputeProviderFlags(cmd)
	return cmd
}
