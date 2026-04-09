package commands

import (
	"encoding/json"
	"os"

	"github.com/getnvoi/nvoi/internal/reconcile"
	"github.com/getnvoi/nvoi/internal/render"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/spf13/cobra"
)

func NewDescribeCmd(dc *reconcile.DeployContext) *cobra.Command {
	return &cobra.Command{
		Use:   "describe",
		Short: "Live cluster state",
		RunE: func(cmd *cobra.Command, args []string) error {
			j, _ := cmd.Flags().GetBool("json")
			req := app.DescribeRequest{Cluster: dc.Cluster}
			if j {
				raw, err := app.DescribeJSON(cmd.Context(), req)
				if err != nil {
					return err
				}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(raw)
			}
			res, err := app.Describe(cmd.Context(), req)
			if err != nil {
				return err
			}
			render.RenderDescribe(res)
			return nil
		},
	}
}

func NewResourcesCmd(dc *reconcile.DeployContext) *cobra.Command {
	return &cobra.Command{
		Use:   "resources",
		Short: "List all provider resources",
		RunE: func(cmd *cobra.Command, args []string) error {
			j, _ := cmd.Flags().GetBool("json")
			groups, err := app.Resources(cmd.Context(), app.ResourcesRequest{
				Compute: app.ProviderRef{Name: dc.Cluster.Provider, Creds: dc.Cluster.Credentials},
				DNS:     dc.DNS,
				Storage: dc.Storage,
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
