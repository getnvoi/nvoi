package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status [deployment-id]",
		Short: "Show deployment status (latest if no ID given)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cfg, err := authedClient()
			if err != nil {
				return err
			}
			wsID, repoID, err := requireRepo(cfg)
			if err != nil {
				return err
			}

			var deploymentID string
			if len(args) > 0 {
				deploymentID = args[0]
			} else {
				// Get latest deployment.
				var list []struct {
					ID string `json:"id"`
				}
				path := "/workspaces/" + wsID + "/repos/" + repoID + "/deployments"
				if err := client.Do("GET", path, nil, &list); err != nil {
					return err
				}
				if len(list) == 0 {
					return fmt.Errorf("no deployments found")
				}
				deploymentID = list[0].ID
			}

			var deployment struct {
				ID     string `json:"id"`
				Status string `json:"status"`
				Steps  []struct {
					Kind   string `json:"kind"`
					Name   string `json:"name"`
					Status string `json:"status"`
					Error  string `json:"error,omitempty"`
				} `json:"steps"`
			}
			path := "/workspaces/" + wsID + "/repos/" + repoID + "/deployments/" + deploymentID
			if err := client.Do("GET", path, nil, &deployment); err != nil {
				return err
			}

			fmt.Printf("deployment %s: %s\n\n", deployment.ID, deployment.Status)
			for _, s := range deployment.Steps {
				marker := " "
				switch s.Status {
				case "succeeded":
					marker = "✓"
				case "failed":
					marker = "✗"
				case "running":
					marker = "▶"
				case "skipped":
					marker = "–"
				}
				line := fmt.Sprintf("  %s %s %s", marker, s.Kind, s.Name)
				if s.Error != "" {
					line += fmt.Sprintf(" (%s)", s.Error)
				}
				fmt.Println(line)
			}
			return nil
		},
	}
}
