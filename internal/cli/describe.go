package cli

import (
	pkgcore "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/internal/render"
	"github.com/spf13/cobra"
)

func newDescribeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "describe",
		Short: "Describe the cluster — nodes, workloads, pods, services, ingress, secrets",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cfg, err := authedClient()
			if err != nil {
				return err
			}
			wsID, repoID, err := requireRepo(cfg)
			if err != nil {
				return err
			}

			var res pkgcore.DescribeResult
			path := "/workspaces/" + wsID + "/repos/" + repoID + "/describe"
			if err := client.Do("GET", path, nil, &res); err != nil {
				return err
			}

			render.RenderDescribe(&res)
			return nil
		},
	}
}
