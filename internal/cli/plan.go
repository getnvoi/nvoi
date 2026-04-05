package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newPlanCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "plan",
		Short: "Show the execution plan for the active repo's latest config",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cfg, err := authedClient()
			if err != nil {
				return err
			}
			wsID, repoID, err := requireRepo(cfg)
			if err != nil {
				return err
			}

			var resp struct {
				Version int `json:"version"`
				Steps   []struct {
					Kind string `json:"kind"`
					Name string `json:"name"`
				} `json:"steps"`
			}
			path := "/workspaces/" + wsID + "/repos/" + repoID + "/config/plan"
			if err := client.Do("GET", path, nil, &resp); err != nil {
				return err
			}

			fmt.Printf("plan for config v%d (%d steps):\n\n", resp.Version, len(resp.Steps))
			for i, s := range resp.Steps {
				fmt.Printf("  %2d. %s %s\n", i+1, s.Kind, s.Name)
			}
			return nil
		},
	}
}
