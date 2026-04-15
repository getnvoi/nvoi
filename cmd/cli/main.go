package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/getnvoi/nvoi/internal/cloud"
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
	// Secrets
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

// mode holds the active backend, populated by PersistentPreRunE.
type mode struct {
	backend Backend
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

	// Shared commands — dispatch to backend with no branching.
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
	addUnauthCloudOnly(root, cloud.NewLoginCmd())
	addCloudOnly(root, cloud.NewWhoamiCmd())
	addCloudOnly(root, cloud.NewWorkspacesCmd())
	addCloudOnly(root, cloud.NewReposCmd())
	addCloudOnly(root, cloud.NewProviderCmd())
	addCloudOnly(root, cloud.NewConfigCmd())

	root.SetErr(newErrorWriter(root))
	root.SetErrPrefix("")

	return root
}

// ── Mode init ───────────────────────────────────────────────────────────────

func initLocal(cmd *cobra.Command, m *mode) error {
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
	m.backend = &localBackend{
		dc:  buildDeployContext(out, cfg),
		cfg: cfg,
		v:   v,
		out: out,
	}
	return nil
}

func initCloud(cmd *cobra.Command, m *mode) error {
	c, authCfg, err := cloud.AuthedClient()
	if err != nil {
		configPath, _ := cmd.Flags().GetString("config")
		if _, statErr := os.Stat(configPath); statErr == nil {
			return fmt.Errorf("not authenticated — run 'nvoi login' for cloud mode, or pass --local for direct mode")
		}
		return fmt.Errorf("not authenticated — run 'nvoi login'")
	}
	ws, repo, err := cloud.RequireRepo(authCfg)
	if err != nil {
		return err
	}
	configPath, _ := cmd.Flags().GetString("config")
	m.backend = &cloudBackend{
		client: c,
		repoPath: func(suffix string) string {
			return "/workspaces/" + ws + "/repos/" + repo + suffix
		},
		out:        resolveOutput(cmd),
		configPath: configPath,
	}
	return nil
}

// ── Cloud-only gates ────────────────────────────────────────────────────────

func addCloudOnly(root *cobra.Command, cmd *cobra.Command) {
	cmd.PersistentPreRunE = func(c *cobra.Command, _ []string) error {
		local, _ := c.Flags().GetBool("local")
		if local {
			return fmt.Errorf("%s is not available in local mode", cmd.Name())
		}
		if _, err := cloud.LoadAuthConfig(); err != nil {
			return err
		}
		return nil
	}
	root.AddCommand(cmd)
}

func addUnauthCloudOnly(root *cobra.Command, cmd *cobra.Command) {
	cmd.PersistentPreRunE = func(c *cobra.Command, _ []string) error {
		local, _ := c.Flags().GetBool("local")
		if local {
			return fmt.Errorf("%s is not available in local mode", cmd.Name())
		}
		return nil
	}
	root.AddCommand(cmd)
}

// ── Shared helpers ──────────────────────────────────────────────────────────

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
