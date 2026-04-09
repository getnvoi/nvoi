package core

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/getnvoi/nvoi/internal/reconcile"
	"github.com/getnvoi/nvoi/internal/render"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// BuildContext builds a DeployContext from viper config + env vars.
func BuildContext(cmd *cobra.Command) *reconcile.DeployContext {
	appName := viper.GetString("app")
	env := viper.GetString("env")
	out := ResolveOutput(cmd)

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

func ResolveOutput(cmd *cobra.Command) app.Output {
	j, _ := cmd.Flags().GetBool("json")
	ci, _ := cmd.Flags().GetBool("ci")
	return render.Resolve(j, ci)
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
	return nil, fmt.Errorf("no SSH key found — set SSH_KEY_PATH or ~/.ssh/id_ed25519")
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
