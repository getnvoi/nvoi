package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/getnvoi/nvoi/internal/commands"
	"github.com/getnvoi/nvoi/internal/reconcile"
	"github.com/getnvoi/nvoi/internal/render"
	app "github.com/getnvoi/nvoi/pkg/core"
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
	appName, env, _ := commands.ResolveAppEnv()
	out := resolveOutput(cmd)

	computeProvider := commands.ResolveProvider("compute")
	computeCreds, _ := commands.ResolveProviderCredentials("compute", computeProvider)
	sshKey, _ := commands.ResolveSSHKey()
	dnsProvider := commands.ResolveProvider("dns")
	dnsCreds, _ := commands.ResolveProviderCredentials("dns", dnsProvider)
	storageProvider := commands.ResolveProvider("storage")
	storageCreds, _ := commands.ResolveProviderCredentials("storage", storageProvider)
	builderName := commands.ResolveProvider("build")
	builderCreds, _ := commands.ResolveProviderCredentials("build", builderName)
	gitUsername, gitToken := commands.ResolveGitAuth()

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
