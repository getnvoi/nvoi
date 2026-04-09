package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"github.com/getnvoi/nvoi/internal/commands"
	"github.com/getnvoi/nvoi/internal/reconcile"
	"github.com/getnvoi/nvoi/internal/render"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/provider"
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
			*dc = *buildContext(cmd)
			return nil
		},
	}

	root.PersistentFlags().String("config", "nvoi.yaml", "path to config YAML")
	root.PersistentFlags().Bool("json", false, "output JSONL")
	root.PersistentFlags().Bool("ci", false, "plain text output")

	root.AddCommand(commands.NewDeployCmd(dc))
	root.AddCommand(commands.NewDestroyCmd(dc))
	root.AddCommand(commands.NewDescribeCmd(dc))
	root.AddCommand(commands.NewResourcesCmd(dc))
	root.AddCommand(commands.NewLogsCmd(dc))
	root.AddCommand(commands.NewExecCmd(dc))
	root.AddCommand(commands.NewSSHCmd(dc))

	root.SetErr(&outputWriter{root: root})
	root.SetErrPrefix("")

	return root
}

func buildContext(cmd *cobra.Command) *reconcile.DeployContext {
	appName := viper.GetString("app")
	env := viper.GetString("env")
	out := resolveOutput(cmd)

	computeProvider := viper.GetString("providers.compute")
	computeCreds, _ := resolveProviderCreds("compute", computeProvider)
	sshKey, _ := resolveSSHKey()
	dnsProvider := viper.GetString("providers.dns")
	dnsCreds, _ := resolveProviderCreds("dns", dnsProvider)
	storageProvider := viper.GetString("providers.storage")
	storageCreds, _ := resolveProviderCreds("storage", storageProvider)
	builderName := viper.GetString("providers.build")
	builderCreds, _ := resolveProviderCreds("build", builderName)
	gitUsername, gitToken := resolveGitAuth()

	return &reconcile.DeployContext{
		Cluster: app.Cluster{
			AppName:     appName,
			Env:         env,
			Provider:    computeProvider,
			Credentials: computeCreds,
			SSHKey:      sshKey,
			Output:      out,
		},
		DNS:         app.ProviderRef{Name: dnsProvider, Creds: dnsCreds},
		Storage:     app.ProviderRef{Name: storageProvider, Creds: storageCreds},
		Builder:     builderName,
		BuildCreds:  builderCreds,
		GitUsername: gitUsername,
		GitToken:    gitToken,
	}
}

func resolveProviderCreds(kind, name string) (map[string]string, error) {
	if name == "" {
		return nil, nil
	}
	schema, err := provider.GetSchema(kind, name)
	if err != nil {
		return nil, err
	}
	creds := make(map[string]string, len(schema.Fields))
	for _, f := range schema.Fields {
		if v := os.Getenv(f.EnvVar); v != "" {
			creds[f.Key] = v
		}
	}
	return creds, nil
}

func resolveSSHKey() ([]byte, error) {
	keyPath := os.Getenv("SSH_KEY_PATH")
	if keyPath != "" {
		keyPath = expandHome(keyPath)
		return os.ReadFile(keyPath)
	}
	home := os.Getenv("HOME")
	for _, name := range []string{"id_ed25519", "id_rsa"} {
		if key, err := os.ReadFile(home + "/.ssh/" + name); err == nil {
			return key, nil
		}
	}
	return nil, fmt.Errorf("no SSH key found — set SSH_KEY_PATH or place a key at ~/.ssh/id_ed25519")
}

func resolveGitAuth() (string, string) {
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

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		if home := os.Getenv("HOME"); home != "" {
			return home + path[1:]
		}
	}
	return path
}

type outputWriter struct{ root *cobra.Command }

func (w *outputWriter) Write(p []byte) (n int, err error) {
	msg := string(p)
	if msg != "" && msg != "\n" {
		msg = strings.TrimSpace(msg)
		msg = strings.TrimPrefix(msg, "Error: ")
		resolveOutput(w.root).Error(fmt.Errorf("%s", msg))
	}
	return len(p), nil
}

func resolveOutput(cmd *cobra.Command) app.Output {
	j, _ := cmd.Flags().GetBool("json")
	ci, _ := cmd.Flags().GetBool("ci")
	return render.Resolve(j, ci)
}
