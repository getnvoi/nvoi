package cli

import (
	"bufio"
	"fmt"
	"time"

	"github.com/getnvoi/nvoi/internal/render"
	pkgcore "github.com/getnvoi/nvoi/pkg/core"
	"github.com/spf13/cobra"
)

func newDeployCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "deploy",
		Short: "Deploy the active repo's latest config",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cfg, err := authedClient()
			if err != nil {
				return err
			}
			wsID, repoID, err := requireRepo(cfg)
			if err != nil {
				return err
			}

			// Create deployment (pending).
			var deployment struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			}
			path := "/workspaces/" + wsID + "/repos/" + repoID + "/deploy"
			if err := client.Do("POST", path, nil, &deployment); err != nil {
				return err
			}

			fmt.Printf("deployment %s\n\n", deployment.ID)

			// Start execution.
			runPath := "/workspaces/" + wsID + "/repos/" + repoID + "/deployments/" + deployment.ID + "/run"
			if err := client.Do("POST", runPath, nil, nil); err != nil {
				return fmt.Errorf("start deployment: %w", err)
			}

			// Poll logs until deployment finishes — render as they come.
			out := render.Resolve(false, false)
			basePath := "/workspaces/" + wsID + "/repos/" + repoID + "/deployments/" + deployment.ID
			lastLines := 0

			for {
				// Check status.
				var status struct {
					Status string `json:"status"`
				}
				if err := client.Do("GET", basePath, nil, &status); err != nil {
					return err
				}

				// Stream new log lines.
				lastLines = streamLogs(client, basePath+"/logs", lastLines, out)

				switch status.Status {
				case "succeeded":
					fmt.Println("\ndeploy succeeded")
					return nil
				case "failed":
					return fmt.Errorf("deploy failed")
				case "pending", "running":
					time.Sleep(2 * time.Second)
				default:
					return fmt.Errorf("unexpected deployment status: %s", status.Status)
				}
			}
		},
	}
}

// streamLogs fetches JSONL logs and renders lines after skip. Returns new total.
func streamLogs(client *APIClient, path string, skip int, out pkgcore.Output) int {
	resp, err := client.doRaw("GET", path)
	if err != nil {
		return skip
	}
	defer resp.Body.Close()

	count := 0
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		count++
		if count <= skip {
			continue
		}
		line := scanner.Text()
		if line != "" {
			render.ReplayLine(line, out)
		}
	}
	return count
}
