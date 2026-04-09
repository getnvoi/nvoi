package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"

	"github.com/getnvoi/nvoi/internal/render"
	pkgcore "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/spf13/cobra"
)

// providerKinds is the single source of truth for the four provider kinds.
var providerKinds = []string{"compute", "dns", "storage", "build"}

func esc(s string) string { return url.PathEscape(s) }

type pathFunc = func(string) string

// ── Deploy / Destroy — send YAML to API, stream JSONL ──────────────────────

func newCloudDeployCmd(client **APIClient, repoPath *pathFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Deploy from config YAML via API",
		RunE: func(cmd *cobra.Command, args []string) error {
			configPath, _ := cmd.Flags().GetString("config")
			if configPath == "" {
				configPath = "nvoi.yaml"
			}
			data, err := os.ReadFile(configPath)
			if err != nil {
				return fmt.Errorf("read config: %w", err)
			}
			return streamRun(*client, (*repoPath)("/deploy"), map[string]any{
				"config": string(data),
			})
		},
	}
	cmd.Flags().String("config", "nvoi.yaml", "path to config YAML")
	return cmd
}

func newCloudDestroyCmd(client **APIClient, repoPath *pathFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "destroy",
		Short: "Destroy all resources via API",
		RunE: func(cmd *cobra.Command, args []string) error {
			configPath, _ := cmd.Flags().GetString("config")
			if configPath == "" {
				configPath = "nvoi.yaml"
			}
			data, err := os.ReadFile(configPath)
			if err != nil {
				return fmt.Errorf("read config: %w", err)
			}
			return streamRun(*client, (*repoPath)("/destroy"), map[string]any{
				"config": string(data),
			})
		},
	}
	cmd.Flags().String("config", "nvoi.yaml", "path to config YAML")
	return cmd
}

// streamRun POSTs a body and streams JSONL response through the TUI.
func streamRun(client *APIClient, path string, body any) error {
	resp, err := client.doRawWithBody("POST", path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	out := render.Resolve(false, false)
	var lastErr error
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		ev, err := pkgcore.ParseEvent(line)
		if err != nil {
			continue
		}
		if ev.Type == pkgcore.EventError {
			lastErr = fmt.Errorf("%s", ev.Message)
		}
		pkgcore.ReplayEvent(ev, out)
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return lastErr
}

// ── Operational commands — direct API calls ─────────────────────────────────

func newCloudDescribeCmd(client **APIClient, repoPath *pathFunc) *cobra.Command {
	return &cobra.Command{
		Use:   "describe",
		Short: "Live cluster state",
		RunE: func(cmd *cobra.Command, args []string) error {
			var res pkgcore.DescribeResult
			if err := (*client).Do("GET", (*repoPath)("/describe"), nil, &res); err != nil {
				return err
			}
			j, _ := cmd.Flags().GetBool("json")
			if j {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(res)
			}
			render.RenderDescribe(&res)
			return nil
		},
	}
}

func newCloudResourcesCmd(client **APIClient, repoPath *pathFunc) *cobra.Command {
	return &cobra.Command{
		Use:   "resources",
		Short: "List all provider resources",
		RunE: func(cmd *cobra.Command, args []string) error {
			var groups []provider.ResourceGroup
			if err := (*client).Do("GET", (*repoPath)("/resources"), nil, &groups); err != nil {
				return err
			}
			j, _ := cmd.Flags().GetBool("json")
			if j {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(groups)
			}
			render.RenderResources(groups)
			return nil
		},
	}
}

func newCloudLogsCmd(client **APIClient, repoPath *pathFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs <service>",
		Short: "Stream service logs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			follow, _ := cmd.Flags().GetBool("follow")
			tail, _ := cmd.Flags().GetInt("tail")
			since, _ := cmd.Flags().GetString("since")
			previous, _ := cmd.Flags().GetBool("previous")
			timestamps, _ := cmd.Flags().GetBool("timestamps")

			path := fmt.Sprintf("/services/%s/logs?tail=%d&since=%s", args[0], tail, since)
			if follow {
				path += "&follow=true"
			}
			if previous {
				path += "&previous=true"
			}
			if timestamps {
				path += "&timestamps=true"
			}
			resp, err := (*client).doRaw("GET", (*repoPath)(path))
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			_, err = io.Copy(os.Stdout, resp.Body)
			return err
		},
	}
	cmd.Flags().BoolP("follow", "f", false, "follow log output")
	cmd.Flags().IntP("tail", "n", 50, "lines from end")
	cmd.Flags().String("since", "", "show logs since duration (e.g. 5m)")
	cmd.Flags().Bool("previous", false, "previous container logs")
	cmd.Flags().Bool("timestamps", false, "include timestamps")
	return cmd
}

func newCloudExecCmd(client **APIClient, repoPath *pathFunc) *cobra.Command {
	return &cobra.Command{
		Use:   "exec <service> -- <command>",
		Short: "Run command in service pod",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := (*client).doRawWithBody("POST", (*repoPath)("/services/"+esc(args[0])+"/exec"), map[string]any{
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

func newCloudSSHCmd(client **APIClient, repoPath *pathFunc) *cobra.Command {
	return &cobra.Command{
		Use:   "ssh -- <command>",
		Short: "Run command on master node",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := (*client).doRawWithBody("POST", (*repoPath)("/ssh"), map[string]any{
				"command": args,
			})
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			scanner := bufio.NewScanner(resp.Body)
			for scanner.Scan() {
				fmt.Println(scanner.Text())
			}
			return scanner.Err()
		},
	}
}
