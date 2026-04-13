package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"syscall"

	"github.com/getnvoi/nvoi/internal/cli"
	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/internal/core"
	"github.com/getnvoi/nvoi/internal/reconcile"
	"github.com/getnvoi/nvoi/internal/render"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	_ "github.com/getnvoi/nvoi/internal/packages/database"

	// Compute
	_ "github.com/getnvoi/nvoi/pkg/provider/compute/aws"
	_ "github.com/getnvoi/nvoi/pkg/provider/compute/hetzner"
	_ "github.com/getnvoi/nvoi/pkg/provider/compute/scaleway"
	// DNS
	_ "github.com/getnvoi/nvoi/pkg/provider/dns/aws"
	_ "github.com/getnvoi/nvoi/pkg/provider/dns/cloudflare"
	_ "github.com/getnvoi/nvoi/pkg/provider/dns/scaleway"
	// Storage
	_ "github.com/getnvoi/nvoi/pkg/provider/storage/aws"
	_ "github.com/getnvoi/nvoi/pkg/provider/storage/cloudflare"
	_ "github.com/getnvoi/nvoi/pkg/provider/storage/scaleway"
	// Build
	_ "github.com/getnvoi/nvoi/pkg/provider/build/daytona"
	_ "github.com/getnvoi/nvoi/pkg/provider/build/github"
	_ "github.com/getnvoi/nvoi/pkg/provider/build/local"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := rootCmd().ExecuteContext(ctx); err != nil {
		os.Exit(1)
	}
}

// mode holds the active backend, populated by PersistentPreRunE.
type mode struct {
	local    bool
	dc       *config.DeployContext // local mode
	client   *cli.APIClient        // cloud mode
	repoPath func(string) string   // cloud mode
}

func rootCmd() *cobra.Command {
	var m mode

	root := &cobra.Command{
		Use:          "nvoi",
		Short:        "Deploy containers to cloud servers",
		SilenceUsage: true,
	}

	root.PersistentFlags().Bool("local", false, "direct mode — run against providers with local credentials")
	root.PersistentFlags().String("config", "nvoi.yaml", "path to config YAML")
	root.PersistentFlags().Bool("json", false, "output JSONL")
	root.PersistentFlags().Bool("ci", false, "plain text output")

	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		local, _ := cmd.Flags().GetBool("local")
		if local {
			return initLocal(cmd, &m)
		}
		return initCloud(cmd, &m)
	}

	// Shared commands — dispatch to local or cloud backend.
	root.AddCommand(newDeployCmd(&m))
	root.AddCommand(newTeardownCmd(&m))
	root.AddCommand(newDescribeCmd(&m))
	root.AddCommand(newResourcesCmd(&m))
	root.AddCommand(newLogsCmd(&m))
	root.AddCommand(newExecCmd(&m))
	root.AddCommand(newSSHCmd(&m))
	root.AddCommand(newCronCmd(&m))
	root.AddCommand(newDatabaseCmd(&m))

	// Cloud-only commands — hard error with --local.
	addCloudOnly(root, cli.NewLoginCmd())
	addCloudOnly(root, cli.NewWhoamiCmd())
	addCloudOnly(root, cli.NewWorkspacesCmd())
	addCloudOnly(root, cli.NewReposCmd())
	addCloudOnly(root, cli.NewProviderCmd())

	return root
}

func initLocal(cmd *cobra.Command, m *mode) error {
	configPath, _ := cmd.Flags().GetString("config")
	viper.SetConfigFile(configPath)
	viper.AutomaticEnv()
	if err := viper.ReadInConfig(); err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	m.local = true
	m.dc = core.BuildContext(cmd)
	return nil
}

func initCloud(cmd *cobra.Command, m *mode) error {
	c, cfg, err := cli.AuthedClient()
	if err != nil {
		configPath, _ := cmd.Flags().GetString("config")
		if _, statErr := os.Stat(configPath); statErr == nil {
			return fmt.Errorf("not authenticated — run 'nvoi login' for cloud mode, or pass --local for direct mode")
		}
		return fmt.Errorf("not authenticated — run 'nvoi login'")
	}
	ws, repo, err := cli.RequireRepo(cfg)
	if err != nil {
		return err
	}
	m.client = c
	m.repoPath = func(suffix string) string {
		return "/workspaces/" + ws + "/repos/" + repo + suffix
	}
	return nil
}

func addCloudOnly(root *cobra.Command, cmd *cobra.Command) {
	cmd.PersistentPreRunE = func(c *cobra.Command, _ []string) error {
		local, _ := c.Flags().GetBool("local")
		if local {
			return fmt.Errorf("%s is not available in local mode", cmd.Name())
		}
		return nil
	}
	root.AddCommand(cmd)
}

func esc(s string) string { return url.PathEscape(s) }

func readConfigFile(cmd *cobra.Command) ([]byte, error) {
	configPath, _ := cmd.Flags().GetString("config")
	if configPath == "" {
		configPath = "nvoi.yaml"
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return data, nil
}

// ── Shared commands ─────────────────────────────────────────────────────────

func newDeployCmd(m *mode) *cobra.Command {
	return &cobra.Command{
		Use:   "deploy",
		Short: "Deploy from config YAML",
		RunE: func(cmd *cobra.Command, args []string) error {
			if m.local {
				cfg, err := core.LoadConfig(cmd)
				if err != nil {
					return err
				}
				return reconcile.Deploy(cmd.Context(), m.dc, cfg, viper.GetViper())
			}
			data, err := readConfigFile(cmd)
			if err != nil {
				return err
			}
			return cli.StreamRun(m.client, m.repoPath("/deploy"), map[string]any{
				"config": string(data),
			})
		},
	}
}

func newTeardownCmd(m *mode) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "teardown",
		Short: "Tear down all provider resources",
		RunE: func(cmd *cobra.Command, args []string) error {
			deleteVolumes, _ := cmd.Flags().GetBool("delete-volumes")
			deleteStorage, _ := cmd.Flags().GetBool("delete-storage")
			if m.local {
				cfg, err := core.LoadConfig(cmd)
				if err != nil {
					return err
				}
				return core.Teardown(cmd.Context(), m.dc, cfg, deleteVolumes, deleteStorage)
			}
			data, err := readConfigFile(cmd)
			if err != nil {
				return err
			}
			return cli.StreamRun(m.client, m.repoPath("/teardown"), map[string]any{
				"config": string(data),
			})
		},
	}
	cmd.Flags().Bool("delete-volumes", false, "also delete persistent volumes (preserved by default)")
	cmd.Flags().Bool("delete-storage", false, "also delete storage buckets (preserved by default)")
	return cmd
}

func newDescribeCmd(m *mode) *cobra.Command {
	return &cobra.Command{
		Use:   "describe",
		Short: "Live cluster state",
		RunE: func(cmd *cobra.Command, args []string) error {
			j, _ := cmd.Flags().GetBool("json")
			if m.local {
				req := app.DescribeRequest{Cluster: m.dc.Cluster}
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
			}
			var res app.DescribeResult
			if err := m.client.Do("GET", m.repoPath("/describe"), nil, &res); err != nil {
				return err
			}
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

func newResourcesCmd(m *mode) *cobra.Command {
	return &cobra.Command{
		Use:   "resources",
		Short: "List all provider resources",
		RunE: func(cmd *cobra.Command, args []string) error {
			j, _ := cmd.Flags().GetBool("json")
			if m.local {
				groups, err := app.Resources(cmd.Context(), app.ResourcesRequest{
					Compute: app.ProviderRef{Name: m.dc.Cluster.Provider, Creds: m.dc.Cluster.Credentials},
					DNS:     m.dc.DNS,
					Storage: m.dc.Storage,
				})
				if err != nil {
					return err
				}
				if j {
					enc := json.NewEncoder(os.Stdout)
					enc.SetIndent("", "  ")
					return enc.Encode(groups)
				}
				render.RenderResources(groups)
				return nil
			}
			var groups []provider.ResourceGroup
			if err := m.client.Do("GET", m.repoPath("/resources"), nil, &groups); err != nil {
				return err
			}
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

func newLogsCmd(m *mode) *cobra.Command {
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
			if m.local {
				return app.Logs(cmd.Context(), app.LogsRequest{
					Cluster: m.dc.Cluster, Service: args[0],
					Follow: follow, Tail: tail, Since: since,
					Previous: previous, Timestamps: timestamps,
				})
			}
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
			resp, err := m.client.DoRaw("GET", m.repoPath(path))
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

func newExecCmd(m *mode) *cobra.Command {
	return &cobra.Command{
		Use:   "exec <service> -- <command>",
		Short: "Run command in service pod",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if m.local {
				return app.Exec(cmd.Context(), app.ExecRequest{
					Cluster: m.dc.Cluster, Service: args[0], Command: args[1:],
				})
			}
			resp, err := m.client.DoRawWithBody("POST", m.repoPath("/services/"+esc(args[0])+"/exec"), map[string]any{
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

func newSSHCmd(m *mode) *cobra.Command {
	return &cobra.Command{
		Use:   "ssh -- <command>",
		Short: "Run command on master node",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if m.local {
				return app.SSH(cmd.Context(), app.SSHRequest{Cluster: m.dc.Cluster, Command: args})
			}
			resp, err := m.client.DoRawWithBody("POST", m.repoPath("/ssh"), map[string]any{
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

func newCronCmd(m *mode) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cron",
		Short: "Manage cron jobs",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "run <name>",
		Short: "Trigger a cron job immediately",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if m.local {
				return app.CronRun(cmd.Context(), app.CronRunRequest{
					Cluster: m.dc.Cluster, Name: args[0],
				})
			}
			return cli.StreamRun(m.client, m.repoPath("/run"), map[string]any{
				"kind": "cron.run",
				"name": args[0],
			})
		},
	})
	return cmd
}

func newDatabaseCmd(m *mode) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "database",
		Aliases: []string{"db"},
		Short:   "Database operations",
	}

	var dbName string
	cmd.PersistentFlags().StringVar(&dbName, "name", "", "database name (defaults to first)")

	backupCmd := &cobra.Command{Use: "backup", Short: "Manage database backups"}

	backupCmd.AddCommand(&cobra.Command{
		Use:   "now",
		Short: "Trigger a backup immediately",
		RunE: func(cmd *cobra.Command, args []string) error {
			if m.local {
				name := core.ResolveDBName(cmd, &dbName)
				return app.CronRun(cmd.Context(), app.CronRunRequest{
					Cluster: m.dc.Cluster, Name: name + "-db-backup",
				})
			}
			name := cloudDBName(&dbName)
			return cli.StreamRun(m.client, m.repoPath("/run"), map[string]any{
				"kind": "cron.run",
				"name": name + "-db-backup",
			})
		},
	})

	backupCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List backups in bucket",
		RunE: func(cmd *cobra.Command, args []string) error {
			if m.local {
				name := core.ResolveDBName(cmd, &dbName)
				return core.DatabaseBackupList(cmd, m.dc, name)
			}
			name := cloudDBName(&dbName)
			var entries []struct {
				Key          string `json:"key"`
				Size         int64  `json:"size"`
				LastModified string `json:"last_modified"`
			}
			if err := m.client.Do("GET", m.repoPath(fmt.Sprintf("/database/backups?name=%s", esc(name))), nil, &entries); err != nil {
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
			if m.local {
				name := core.ResolveDBName(cmd, &dbName)
				outFile, _ := cmd.Flags().GetString("file")
				return core.DatabaseBackupDownload(cmd, m.dc, name, args[0], outFile)
			}
			name := cloudDBName(&dbName)
			outFile, _ := cmd.Flags().GetString("file")
			resp, err := m.client.DoRaw("GET", m.repoPath(fmt.Sprintf("/database/backups/%s?name=%s", esc(args[0]), esc(name))))
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

	cmd.AddCommand(&cobra.Command{
		Use:   "sql <query>",
		Short: "Run SQL against the database",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if m.local {
				name := core.ResolveDBName(cmd, &dbName)
				return core.DatabaseSQL(cmd, m.dc, name, args[0])
			}
			name := cloudDBName(&dbName)
			var result struct {
				Output string `json:"output"`
			}
			if err := m.client.Do("POST", m.repoPath("/database/sql"), map[string]any{
				"name":  name,
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

func cloudDBName(dbName *string) string {
	if *dbName != "" {
		return *dbName
	}
	return "main"
}
