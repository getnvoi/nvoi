package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/getnvoi/nvoi/internal/core"
	"github.com/getnvoi/nvoi/internal/reconcile"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	_ "github.com/getnvoi/nvoi/pkg/provider/aws"
	_ "github.com/getnvoi/nvoi/pkg/provider/cloudflare"
	_ "github.com/getnvoi/nvoi/pkg/provider/daytona"
	_ "github.com/getnvoi/nvoi/pkg/provider/github"
	_ "github.com/getnvoi/nvoi/pkg/provider/hetzner"
	_ "github.com/getnvoi/nvoi/pkg/provider/local"
	_ "github.com/getnvoi/nvoi/pkg/provider/scaleway"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := rootCmd().ExecuteContext(ctx); err != nil {
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	dc := &reconcile.DeployContext{}

	root := &cobra.Command{
		Use:          "nvoi",
		Short:        "Deploy containers to cloud servers",
		SilenceUsage: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			configPath, _ := cmd.Root().Flags().GetString("config")
			viper.SetConfigFile(configPath)
			viper.AutomaticEnv()
			if err := viper.ReadInConfig(); err != nil {
				return fmt.Errorf("read config: %w", err)
			}
			*dc = *core.BuildContext(cmd)
			return nil
		},
	}

	root.PersistentFlags().String("config", "nvoi.yaml", "path to config YAML")
	root.PersistentFlags().Bool("json", false, "output JSONL")
	root.PersistentFlags().Bool("ci", false, "plain text output")

	root.AddCommand(core.NewDeployCmd(dc))
	root.AddCommand(core.NewDestroyCmd(dc))
	root.AddCommand(core.NewDescribeCmd(dc))
	root.AddCommand(core.NewResourcesCmd(dc))
	root.AddCommand(core.NewLogsCmd(dc))
	root.AddCommand(core.NewExecCmd(dc))
	root.AddCommand(core.NewSSHCmd(dc))

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
		core.ResolveOutput(w.root).Error(fmt.Errorf("%s", msg))
	}
	return len(p), nil
}
