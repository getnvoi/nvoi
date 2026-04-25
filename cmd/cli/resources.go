package main

import (
	"encoding/json"
	"os"

	"github.com/getnvoi/nvoi/internal/render"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/spf13/cobra"
)

func newResourcesCmd(rt *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "resources",
		Short: "List all provider resources",
		RunE: func(cmd *cobra.Command, args []string) error {
			j, _ := cmd.Flags().GetBool("json")
			groups, err := app.Resources(cmd.Context(), app.ResourcesRequest{
				Infra:   app.ProviderRef{Name: rt.dc.Cluster.Provider, Creds: rt.dc.Cluster.Credentials},
				DNS:     rt.dc.DNS,
				Storage: rt.dc.Storage,
				Tunnel:  rt.dc.Tunnel,
			})
			if err != nil {
				return err
			}
			if j {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(groups)
			}
			render.RenderResources(groups)
			return nil
		},
	}
}
