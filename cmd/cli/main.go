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

	// Providers — each vendor package registers all its kinds via init().
	_ "github.com/getnvoi/nvoi/pkg/provider/aws"
	_ "github.com/getnvoi/nvoi/pkg/provider/cloudflare"
	_ "github.com/getnvoi/nvoi/pkg/provider/github"
	_ "github.com/getnvoi/nvoi/pkg/provider/hetzner"
	_ "github.com/getnvoi/nvoi/pkg/provider/ngrok"
	_ "github.com/getnvoi/nvoi/pkg/provider/scaleway"
	// Build backends — `providers.build` family. local = in-process
	// default; ssh dispatches to a role: builder server over SSH;
	// daytona runs the build inside a managed DinD sandbox.
	_ "github.com/getnvoi/nvoi/pkg/provider/build/daytona"
	_ "github.com/getnvoi/nvoi/pkg/provider/build/local"
	_ "github.com/getnvoi/nvoi/pkg/provider/build/ssh"
	// Secrets backends
	_ "github.com/getnvoi/nvoi/pkg/provider/secrets/awssm"
	_ "github.com/getnvoi/nvoi/pkg/provider/secrets/doppler"
	_ "github.com/getnvoi/nvoi/pkg/provider/secrets/infisical"
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
	out app.Output
}

func rootCmd() *cobra.Command {
	var rt runtime

	root := &cobra.Command{
		Use:          "nvoi",
		Short:        "Reconcile cloud infrastructure and Kubernetes workloads from one YAML",
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
	root.AddCommand(newCICmd(&rt))

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
	out := resolveOutput(cmd)
	dc, err := buildDeployContext(cmd.Context(), out, cfg)
	if err != nil {
		return err
	}
	rt.cfg = cfg
	rt.out = out
	rt.dc = dc
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
