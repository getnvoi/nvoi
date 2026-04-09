package cli

import (
	"bufio"

	"github.com/getnvoi/nvoi/internal/render"
	"github.com/spf13/cobra"
)

func newDeployLogsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "deploy-logs <deployment-id>",
		Short: "Stream deployment logs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cfg, err := authedClient()
			if err != nil {
				return err
			}
			wsID, repoID, err := requireRepo(cfg)
			if err != nil {
				return err
			}

			path := "/workspaces/" + wsID + "/repos/" + repoID + "/deployments/" + esc(args[0]) + "/logs"

			resp, err := client.doRaw("GET", path)
			if err != nil {
				return err
			}
			defer resp.Body.Close()

			out := render.Resolve(false, false)
			scanner := bufio.NewScanner(resp.Body)
			for scanner.Scan() {
				line := scanner.Text()
				if line != "" {
					render.ReplayLine(line, out)
				}
			}
			return scanner.Err()
		},
	}
}
