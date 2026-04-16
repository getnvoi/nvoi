package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/internal/render"
	app "github.com/getnvoi/nvoi/pkg/core"
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

// runtime holds everything a command needs, populated by PersistentPreRunE.
type runtime struct {
	dc  *config.DeployContext
	cfg *config.AppConfig
	v   *viper.Viper
	out app.Output
}

func rootCmd() *cobra.Command {
	var rt runtime

	root := &cobra.Command{
		Use:          "nvoi",
		Short:        "Deploy containers to cloud servers",
		SilenceUsage: true,
	}

	root.PersistentFlags().String("config", "nvoi.yaml", "path to config YAML")
	root.PersistentFlags().Bool("json", false, "output JSONL")
	root.PersistentFlags().Bool("ci", false, "plain text output")

	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		return initRuntime(cmd, &rt)
	}

	root.AddCommand(newDeployCmd(&rt))
	root.AddCommand(newTeardownCmd(&rt))
	root.AddCommand(newDescribeCmd(&rt))
	root.AddCommand(newResourcesCmd(&rt))
	root.AddCommand(newLogsCmd(&rt))
	root.AddCommand(newExecCmd(&rt))
	root.AddCommand(newSSHCmd(&rt))
	root.AddCommand(newCronCmd(&rt))
	root.AddCommand(newDatabaseCmd(&rt))

	root.SetErr(newErrorWriter(root))
	root.SetErrPrefix("")

	return root
}

// initRuntime loads config and builds the deploy context from env-resolved credentials.
func initRuntime(cmd *cobra.Command, rt *runtime) error {
	configPath, _ := cmd.Flags().GetString("config")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", configPath, err)
	}
	cfg, err := config.ParseAppConfig(data)
	if err != nil {
		return err
	}
	v := viper.New()
	v.AutomaticEnv()
	out := resolveOutput(cmd)
	rt.cfg = cfg
	rt.v = v
	rt.out = out
	rt.dc = buildDeployContext(out, cfg)
	return nil
}

func resolveOutput(cmd *cobra.Command) app.Output {
	j, _ := cmd.Flags().GetBool("json")
	ci, _ := cmd.Flags().GetBool("ci")
	return render.Resolve(j, ci)
}

type errorWriter struct{ root *cobra.Command }

func newErrorWriter(root *cobra.Command) *errorWriter {
	return &errorWriter{root: root}
}

func (w *errorWriter) Write(p []byte) (n int, err error) {
	msg := string(p)
	if msg != "" && msg != "\n" {
		msg = strings.TrimSpace(msg)
		msg = strings.TrimPrefix(msg, "Error: ")
		resolveOutput(w.root).Error(errors.New(msg))
	}
	return len(p), nil
}
