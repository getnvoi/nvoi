package core

import (
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/internal/commands"
	"github.com/getnvoi/nvoi/internal/render"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/spf13/cobra"
)

func Root() *cobra.Command {
	d := &DirectBackend{}

	root := &cobra.Command{
		Use:          "nvoi",
		Short:        "Deploy containers to cloud servers",
		SilenceUsage: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			*d = *buildDirectBackend(cmd)
			return nil
		},
	}

	// Persistent flags — provider selection + output.
	root.PersistentFlags().String("app-name", "", "application name (env: NVOI_APP_NAME)")
	root.PersistentFlags().String("environment", "", "environment (env: NVOI_ENV)")
	root.PersistentFlags().String("compute-provider", "", "compute provider (hetzner, aws, scaleway)")
	root.PersistentFlags().StringArray("compute-credentials", nil, "compute provider credentials (KEY=VALUE)")
	root.PersistentFlags().String("dns-provider", "", "DNS provider (cloudflare, aws)")
	root.PersistentFlags().String("storage-provider", "", "storage provider (cloudflare, aws)")
	root.PersistentFlags().String("build-provider", "", "build provider (local, daytona, github)")
	root.PersistentFlags().StringArray("build-credentials", nil, "build provider credentials (KEY=VALUE)")
	root.PersistentFlags().String("zone", "", "DNS zone (env: DNS_ZONE)")
	root.PersistentFlags().String("git-token", "", "git token for private repo cloning")
	root.PersistentFlags().String("env-file", ".env", "path to .env file")
	root.PersistentFlags().Bool("json", false, "output JSONL")
	root.PersistentFlags().Bool("ci", false, "plain text output for CI/logs")

	// Infrastructure.
	root.AddCommand(commands.NewInstanceCmd(d))
	root.AddCommand(commands.NewFirewallCmd(d))
	root.AddCommand(commands.NewVolumeCmd(d))
	root.AddCommand(commands.NewDNSCmd(d))
	root.AddCommand(commands.NewIngressCmd(d))
	root.AddCommand(commands.NewStorageCmd(d))

	// Application.
	root.AddCommand(commands.NewServiceCmd(d))
	root.AddCommand(commands.NewCronCmd(d))
	root.AddCommand(commands.NewSecretCmd(d))

	// Managed categories.
	root.AddCommand(commands.NewDatabaseCmd(d))
	root.AddCommand(commands.NewAgentCmd(d))

	// Build.
	root.AddCommand(commands.NewBuildCmd(d))

	// Live view.
	root.AddCommand(commands.NewDescribeCmd(d))

	// Operate.
	root.AddCommand(commands.NewLogsCmd(d))
	root.AddCommand(commands.NewExecCmd(d))
	root.AddCommand(commands.NewSSHCmd(d))

	// Inspect.
	root.AddCommand(commands.NewResourcesCmd(d))

	// Style errors through Output.
	root.SetErr(&outputWriter{root: root})
	root.SetErrPrefix("")

	return root
}

// buildDirectBackend resolves all providers and credentials best-effort.
// Missing providers are left empty — methods fail naturally when they need them.
func buildDirectBackend(cmd *cobra.Command) *DirectBackend {
	appName, env, _ := resolveAppEnv(cmd)
	out := resolveOutput(cmd)

	computeProvider, _ := resolveComputeProvider(cmd)
	var computeCreds map[string]string
	if computeProvider != "" {
		computeCreds, _ = resolveComputeCredentials(cmd, computeProvider)
	}

	sshKey, _ := resolveSSHKey()

	dnsProvider, _ := resolveDNSProvider(cmd)
	var dnsCreds map[string]string
	if dnsProvider != "" {
		dnsCreds, _ = resolveDNSCredentials(cmd, dnsProvider)
	}

	storageProvider, _ := resolveStorageProvider(cmd)
	var storageCreds map[string]string
	if storageProvider != "" {
		storageCreds, _ = resolveStorageCredentials(cmd, storageProvider)
	}

	builderName, _ := resolveBuildProvider(cmd)
	var builderCreds map[string]string
	if builderName != "" {
		builderCreds, _ = resolveBuildCredentials(cmd, builderName)
	}

	gitUsername, gitToken := resolveGitAuth(cmd)

	return &DirectBackend{
		cluster: app.Cluster{
			AppName:     appName,
			Env:         env,
			Provider:    computeProvider,
			Credentials: computeCreds,
			SSHKey:      sshKey,
			Output:      out,
		},
		dns:          app.ProviderRef{Name: dnsProvider, Creds: dnsCreds},
		storage:      app.ProviderRef{Name: storageProvider, Creds: storageCreds},
		builder:      builderName,
		builderCreds: builderCreds,
		gitUsername:  gitUsername,
		gitToken:     gitToken,
	}
}

// outputWriter wraps cobra's error output through our Output system.
type outputWriter struct {
	root *cobra.Command
}

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
