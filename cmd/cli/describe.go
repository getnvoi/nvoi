package main

import (
	"encoding/json"
	"os"

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
			// Workloads = every service + cron from cfg, sorted. describe
			// walks the live `{name}-secrets` Secret for each so auto-
			// injected keys (DATABASE_URL_X, storage creds) surface
			// alongside explicit secrets: declarations.
			workloads := append(utils.SortedKeys(rt.cfg.Services), utils.SortedKeys(rt.cfg.Crons)...)

			req := app.DescribeRequest{
				Cluster:      rt.dc.Cluster,
				Cfg:          config.NewView(rt.cfg),
				StorageNames: rt.cfg.StorageNames(),
				Workloads:    workloads,
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
