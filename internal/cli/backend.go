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

// PathFunc builds a repo-scoped API path from a suffix.
type PathFunc = func(string) string

// ── Deploy / Destroy — send YAML to API, stream JSONL ──────────────────────

func NewDeployCmd(client **APIClient, repoPath *PathFunc) *cobra.Command {
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
			return StreamRun(*client, (*repoPath)("/deploy"), map[string]any{
				"config": string(data),
			})
		},
	}
	cmd.Flags().String("config", "nvoi.yaml", "path to config YAML")
	return cmd
}

func NewTeardownCmd(client **APIClient, repoPath *PathFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "teardown",
		Short: "Tear down all resources via API",
		RunE: func(cmd *cobra.Command, args []string) error {
			configPath, _ := cmd.Flags().GetString("config")
			if configPath == "" {
				configPath = "nvoi.yaml"
			}
			data, err := os.ReadFile(configPath)
			if err != nil {
				return fmt.Errorf("read config: %w", err)
			}
			return StreamRun(*client, (*repoPath)("/teardown"), map[string]any{
				"config": string(data),
			})
		},
	}
	cmd.Flags().String("config", "nvoi.yaml", "path to config YAML")
	return cmd
}

// StreamRun POSTs a body and streams JSONL response through the TUI.
func StreamRun(client *APIClient, path string, body any) error {
	resp, err := client.DoRawWithBody("POST", path, body)
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

func NewDescribeCmd(client **APIClient, repoPath *PathFunc) *cobra.Command {
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

func NewResourcesCmd(client **APIClient, repoPath *PathFunc) *cobra.Command {
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

func NewLogsCmd(client **APIClient, repoPath *PathFunc) *cobra.Command {
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
			resp, err := (*client).DoRaw("GET", (*repoPath)(path))
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

func NewExecCmd(client **APIClient, repoPath *PathFunc) *cobra.Command {
	return &cobra.Command{
		Use:   "exec <service> -- <command>",
		Short: "Run command in service pod",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := (*client).DoRawWithBody("POST", (*repoPath)("/services/"+esc(args[0])+"/exec"), map[string]any{
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

func NewCronCmd(client **APIClient, repoPath *PathFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cron",
		Short: "Manage cron jobs",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "run <name>",
		Short: "Trigger a cron job immediately",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return StreamRun(*client, (*repoPath)("/run"), map[string]any{
				"kind": "cron.run",
				"name": args[0],
			})
		},
	})
	return cmd
}

func NewDatabaseCmd(client **APIClient, repoPath *PathFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "database",
		Aliases: []string{"db"},
		Short:   "Database operations",
	}

	var dbName string
	cmd.PersistentFlags().StringVar(&dbName, "name", "main", "database name")

	// db backup
	backupCmd := &cobra.Command{Use: "backup", Short: "Manage database backups"}

	backupCmd.AddCommand(&cobra.Command{
		Use:   "now",
		Short: "Trigger a backup immediately",
		RunE: func(cmd *cobra.Command, args []string) error {
			cronName := dbName + "-db-backup"
			return StreamRun(*client, (*repoPath)("/run"), map[string]any{
				"kind": "cron.run",
				"name": cronName,
			})
		},
	})

	backupCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List backups in bucket",
		RunE: func(cmd *cobra.Command, args []string) error {
			var entries []struct {
				Key          string `json:"key"`
				Size         int64  `json:"size"`
				LastModified string `json:"last_modified"`
			}
			path := (*repoPath)(fmt.Sprintf("/database/backups?name=%s", esc(dbName)))
			if err := (*client).Do("GET", path, nil, &entries); err != nil {
				return err
			}
			if len(entries) == 0 {
				fmt.Println("no backups found")
				return nil
			}
			for _, e := range entries {
				fmt.Printf("%s  %s  %d bytes\n", e.LastModified, e.Key, e.Size)
			}
			return nil
		},
	})

	dlCmd := &cobra.Command{
		Use:   "download <backup-name>",
		Short: "Download a backup file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			outFile, _ := cmd.Flags().GetString("file")
			path := (*repoPath)(fmt.Sprintf("/database/backups/%s?name=%s", esc(args[0]), esc(dbName)))
			resp, err := (*client).DoRaw("GET", path)
			if err != nil {
				return err
			}
			defer resp.Body.Close()

			var w io.Writer = os.Stdout
			if outFile != "" {
				f, err := os.Create(outFile)
				if err != nil {
					return err
				}
				defer f.Close()
				w = f
			}
			n, err := io.Copy(w, resp.Body)
			if err != nil {
				return err
			}
			if outFile != "" {
				fmt.Printf("downloaded %s (%d bytes)\n", outFile, n)
			}
			return nil
		},
	}
	dlCmd.Flags().StringP("file", "f", "", "output file (default: stdout)")
	backupCmd.AddCommand(dlCmd)

	cmd.AddCommand(backupCmd)

	// db sql
	cmd.AddCommand(&cobra.Command{
		Use:   "sql <query>",
		Short: "Run SQL against the database",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var result struct {
				Output string `json:"output"`
			}
			if err := (*client).Do("POST", (*repoPath)("/database/sql"), map[string]any{
				"name":  dbName,
				"query": args[0],
			}, &result); err != nil {
				return err
			}
			fmt.Print(result.Output)
			return nil
		},
	})

	return cmd
}

func NewSSHCmd(client **APIClient, repoPath *PathFunc) *cobra.Command {
	return &cobra.Command{
		Use:   "ssh -- <command>",
		Short: "Run command on master node",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := (*client).DoRawWithBody("POST", (*repoPath)("/ssh"), map[string]any{
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
