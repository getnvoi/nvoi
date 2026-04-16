package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/getnvoi/nvoi/internal/agent"
	"github.com/getnvoi/nvoi/internal/config"
	"github.com/spf13/cobra"
)

func newAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Start the nvoi agent — the deploy runtime on the master node",
		Long: `Starts a long-running HTTP server on localhost that accepts deploy
commands and streams JSONL results. The agent holds credentials, executes
all operations, and reports to the API if configured.

The CLI and API are clients — they send commands to the agent.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			configPath, _ := cmd.Flags().GetString("config")
			addr, _ := cmd.Flags().GetString("addr")

			data, err := os.ReadFile(configPath)
			if err != nil {
				return fmt.Errorf("read config %s: %w", configPath, err)
			}
			cfg, err := config.ParseAppConfig(data)
			if err != nil {
				return fmt.Errorf("parse config: %v", err)
			}

			a := agent.New(cmd.Context(), cfg)

			mux := http.NewServeMux()
			a.RegisterRoutes(mux)

			srv := &http.Server{Addr: addr, Handler: mux}

			go func() {
				<-cmd.Context().Done()
				srv.Close()
			}()

			fmt.Fprintf(os.Stderr, "nvoi agent listening on %s (app=%s env=%s)\n", addr, cfg.App, cfg.Env)
			if err := srv.ListenAndServe(); err != http.ErrServerClosed {
				return fmt.Errorf("agent server: %w", err)
			}
			return nil
		},
	}

	cmd.Flags().String("addr", "127.0.0.1:9500", "listen address")
	cmd.Flags().String("config", "nvoi.yaml", "path to config YAML")

	return cmd
}
