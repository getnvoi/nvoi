package main

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/getnvoi/nvoi/internal/agent"
	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/pkg/kube"
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

			// Resolve env-dependent values here — cmd/ is the os.Getenv boundary.
			sshKey, err := resolveAgentSSHKey()
			if err != nil {
				return err
			}
			gitUsername, gitToken := resolveAgentGitAuth()

			// Create k8s client — agent runs on master, direct to localhost:6443.
			kubeClient, err := kube.NewLocal("")
			if err != nil {
				return fmt.Errorf("kube client: %w", err)
			}

			a := agent.New(cmd.Context(), cfg, agent.AgentOpts{
				SSHKey:      sshKey,
				GitUsername: gitUsername,
				GitToken:    gitToken,
				Kube:        kubeClient,
			})

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

// resolveAgentSSHKey reads the SSH private key from disk.
// os.Getenv lives here — the cmd/ boundary.
func resolveAgentSSHKey() ([]byte, error) {
	keyPath := os.Getenv("SSH_KEY_PATH")
	if keyPath != "" {
		if strings.HasPrefix(keyPath, "~/") {
			if home := os.Getenv("HOME"); home != "" {
				keyPath = home + keyPath[1:]
			}
		}
		return os.ReadFile(keyPath)
	}
	home := os.Getenv("HOME")
	for _, name := range []string{"id_ed25519", "id_rsa"} {
		if key, err := os.ReadFile(home + "/.ssh/" + name); err == nil {
			return key, nil
		}
	}
	return nil, fmt.Errorf("no SSH key found — set SSH_KEY_PATH or ~/.ssh/id_ed25519")
}

// resolveAgentGitAuth resolves git credentials from gh CLI or env.
// os.Getenv lives here — the cmd/ boundary.
func resolveAgentGitAuth() (string, string) {
	if out, err := exec.Command("gh", "auth", "token").Output(); err == nil {
		if token := strings.TrimSpace(string(out)); token != "" {
			return "x-access-token", token
		}
	}
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		return "x-access-token", token
	}
	return "", ""
}
