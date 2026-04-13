package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/internal/core"
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

func rootCmd() *cobra.Command {
	dc := &config.DeployContext{}
	var cfg *config.AppConfig

	root := &cobra.Command{
		Use:          "nvoi",
		Short:        "Deploy containers to cloud servers",
		SilenceUsage: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			c, err := core.LoadConfig(cmd)
			if err != nil {
				return err
			}
			cfg = c
			viper.AutomaticEnv()
			*dc = *core.BuildContextFromConfig(cmd, cfg)
			return nil
		},
	}

	root.PersistentFlags().String("config", "nvoi.yaml", "path to config YAML")
	root.PersistentFlags().Bool("json", false, "output JSONL")
	root.PersistentFlags().Bool("ci", false, "plain text output")

	root.AddCommand(core.NewDeployCmd(dc, &cfg))
	root.AddCommand(core.NewTeardownCmd(dc, &cfg))
	root.AddCommand(core.NewDescribeCmd(dc))
	root.AddCommand(core.NewResourcesCmd(dc))
	root.AddCommand(core.NewLogsCmd(dc))
	root.AddCommand(core.NewExecCmd(dc))
	root.AddCommand(core.NewSSHCmd(dc))
	root.AddCommand(core.NewCronCmd(dc))
	root.AddCommand(core.NewDatabaseCmd(dc, &cfg))

	root.SetErr(&outputWriter{root: root})
	root.SetErrPrefix("")

	return root
}

type outputWriter struct{ root *cobra.Command }

func (w *outputWriter) Write(p []byte) (n int, err error) {
	msg := string(p)
	if msg != "" && msg != "\n" {
		msg = strings.TrimSpace(msg)
		msg = strings.TrimPrefix(msg, "Error: ")
		core.ResolveOutput(w.root).Error(errors.New(msg))
	}
	return len(p), nil
}
