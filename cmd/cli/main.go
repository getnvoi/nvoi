package main

import (
	"bufio"
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
	cleanup func() // SSH tunnel cleanup — called after command completes
}

func rootCmd() *cobra.Command {
	var m mode

	root := &cobra.Command{
		Use:          "nvoi",
		Short:        "Deploy containers to cloud servers",
		SilenceUsage: true,
		PersistentPostRun: func(_ *cobra.Command, _ []string) {
			if m.cleanup != nil {
				m.cleanup()
			}
		},
	}

	root.PersistentFlags().String("config", "nvoi.yaml", "path to config YAML")
	root.PersistentFlags().Bool("json", false, "output JSONL")
	root.PersistentFlags().Bool("ci", false, "plain text output")
	root.PersistentFlags().BoolP("yes", "y", false, "skip confirmation prompts")

	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		return initBackend(cmd, &m)
	}

	// Commands — all dispatch to backend (agent or local bootstrap).
	root.AddCommand(newDeployCmd(&m))
	root.AddCommand(newTeardownCmd(&m))
	root.AddCommand(newDescribeCmd(&m))
	root.AddCommand(newResourcesCmd(&m))
	root.AddCommand(newLogsCmd(&m))
	root.AddCommand(newExecCmd(&m))
	root.AddCommand(newSSHCmd(&m))
	root.AddCommand(newCronCmd(&m))
	root.AddCommand(newDatabaseCmd(&m))

	// Agent command — standalone, no backend init.
	agentCmd := newAgentCmd()
	agentCmd.PersistentPreRunE = func(_ *cobra.Command, _ []string) error { return nil }
	root.AddCommand(agentCmd)

	root.SetErr(newErrorWriter(root))
	root.SetErrPrefix("")

	return root
}

// ── Backend init ────────────────────────────────────────────────────────────
// One path. Try agent first. If no master exists, bootstrap locally.

func initBackend(cmd *cobra.Command, m *mode) error {
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

	// Try connecting to the agent on the master.
	backend, cleanup, err := connectToAgent(cmd.Context(), out, cfg, configPath)
	if err == nil {
		m.backend = backend
		m.cleanup = cleanup
		return nil
	}

	// Agent unreachable. If the master doesn't exist, bootstrap.
	// If the master exists but agent is down, that's a real error.
	if !errors.Is(err, ErrNoMaster) {
		return fmt.Errorf("agent not reachable: %w", err)
	}

	// No master — first deploy. Confirm with user unless -y.
	yes, _ := cmd.Flags().GetBool("yes")
	if !yes {
		fmt.Fprintf(os.Stderr, "No existing cluster found. Create servers and deploy? [y/N] ")
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Scan()
		if answer := strings.TrimSpace(strings.ToLower(scanner.Text())); answer != "y" && answer != "yes" {
			return fmt.Errorf("aborted")
		}
	}

	// Bootstrap: run locally, install agent as part of provisioning.
	lb, lbErr := newLocalBackend(cmd.Context(), out, cfg)
	if lbErr != nil {
		return lbErr
	}
	m.backend = lb
	return nil
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
