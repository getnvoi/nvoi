package cli

import (
	"github.com/getnvoi/nvoi/internal/render"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/spf13/cobra"
)

func newResourcesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resources",
		Short: "List all provider resources",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cfg, err := authedClient()
			if err != nil {
				return err
			}
			wsID, repoID, err := requireRepo(cfg)
			if err != nil {
				return err
			}

			var groups []provider.ResourceGroup
			path := "/workspaces/" + wsID + "/repos/" + repoID + "/resources"
			if err := client.Do("GET", path, nil, &groups); err != nil {
				return err
			}

			render.RenderResources(groups)
			return nil
		},
	}
}
