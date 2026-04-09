package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	pkgcore "github.com/getnvoi/nvoi/pkg/core"
	"github.com/spf13/cobra"
)

func newAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Manage agents",
	}
	cmd.AddCommand(newCloudAgentListCmd())
	cmd.AddCommand(newCloudAgentExecCmd())
	cmd.AddCommand(newCloudAgentLogsCmd())
	return cmd
}

func newCloudAgentListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List managed agents",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cfg, err := authedClient()
			if err != nil {
				return err
			}
			wsID, repoID, err := requireRepo(cfg)
			if err != nil {
				return err
			}

			var services []pkgcore.ManagedService
			path := "/workspaces/" + wsID + "/repos/" + repoID + "/agent"
			if err := client.Do("GET", path, nil, &services); err != nil {
				return err
			}

			if len(services) == 0 {
				fmt.Println("no managed agents found")
				return nil
			}
			for _, svc := range services {
				children := strings.Join(svc.Children, ", ")
				fmt.Printf("%s  type=%s  %s  %s  children=[%s]\n", svc.Name, svc.ManagedKind, svc.Image, svc.Ready, children)
			}
			return nil
		},
	}
}

func newCloudAgentExecCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "exec [name] -- [command...]",
		Short: "Run a command in an agent pod",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cfg, err := authedClient()
			if err != nil {
				return err
			}
			wsID, repoID, err := requireRepo(cfg)
			if err != nil {
				return err
			}

			path := "/workspaces/" + wsID + "/repos/" + repoID + "/agent/" + args[0] + "/exec"
			resp, err := client.doRawWithBody("POST", path, map[string]any{
				"command": args[1:],
			})
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			_, err = io.Copy(os.Stdout, resp.Body)
			return err
		},
	}
}

func newCloudAgentLogsCmd() *cobra.Command {
	logsCmd := &cobra.Command{
		Use:   "logs [name]",
		Short: "Show agent logs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			follow, _ := cmd.Flags().GetBool("follow")
			tail, _ := cmd.Flags().GetInt("tail")
			since, _ := cmd.Flags().GetString("since")
			previous, _ := cmd.Flags().GetBool("previous")
			timestamps, _ := cmd.Flags().GetBool("timestamps")

			client, cfg, err := authedClient()
			if err != nil {
				return err
			}
			wsID, repoID, err := requireRepo(cfg)
			if err != nil {
				return err
			}

			path := fmt.Sprintf("/workspaces/%s/repos/%s/agent/%s/logs?tail=%d&since=%s",
				wsID, repoID, args[0], tail, since)
			if follow {
				path += "&follow=true"
			}
			if previous {
				path += "&previous=true"
			}
			if timestamps {
				path += "&timestamps=true"
			}

			resp, err := client.doRaw("GET", path)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			_, err = io.Copy(os.Stdout, resp.Body)
			return err
		},
	}
	logsCmd.Flags().BoolP("follow", "f", false, "follow log output")
	logsCmd.Flags().IntP("tail", "n", 50, "number of lines to show")
	logsCmd.Flags().String("since", "", "show logs since duration (5m, 1h)")
	logsCmd.Flags().Bool("previous", false, "show previous container logs")
	logsCmd.Flags().Bool("timestamps", false, "show timestamps")
	return logsCmd
}
