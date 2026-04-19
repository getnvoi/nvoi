package main

import (
	"encoding/json"
	"os"
	"sort"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/internal/render"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/utils"
	"github.com/spf13/cobra"
)

func newDescribeCmd(rt *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "describe",
		Short: "Live cluster state",
		RunE: func(cmd *cobra.Command, args []string) error {
			j, _ := cmd.Flags().GetBool("json")
			// Build config-derived tunnel routes so describe can show them
			// without needing to reach out to the provider API.
			var tunnelRoutes []app.DescribeIngress
			if rt.cfg.Providers.Tunnel != "" {
				for _, svcName := range utils.SortedKeys(rt.cfg.Domains) {
					svc, ok := rt.cfg.Services[svcName]
					if !ok {
						continue
					}
					for _, domain := range rt.cfg.Domains[svcName] {
						tunnelRoutes = append(tunnelRoutes, app.DescribeIngress{
							Domain: domain, Service: svcName, Port: svc.Port,
						})
					}
				}
				sort.Slice(tunnelRoutes, func(i, j int) bool {
					return tunnelRoutes[i].Domain < tunnelRoutes[j].Domain
				})
			}

			req := app.DescribeRequest{
				Cluster:        rt.dc.Cluster,
				Cfg:            config.NewView(rt.cfg),
				StorageNames:   rt.cfg.StorageNames(),
				ServiceSecrets: rt.cfg.ServiceSecrets(),
				TunnelProvider: rt.cfg.Providers.Tunnel,
				TunnelRoutes:   tunnelRoutes,
			}
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
