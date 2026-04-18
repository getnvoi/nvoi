package main

import (
	"encoding/json"
	"os"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/internal/render"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/spf13/cobra"
)

func newDescribeCmd(rt *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "describe",
		Short: "Live cluster state",
		RunE: func(cmd *cobra.Command, args []string) error {
			j, _ := cmd.Flags().GetBool("json")
			req := app.DescribeRequest{
				Cluster:        rt.dc.Cluster,
				Cfg:            config.NewView(rt.cfg),
				StorageNames:   rt.cfg.StorageNames(),
				ServiceSecrets: rt.cfg.ServiceSecrets(),
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
