package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/getnvoi/nvoi/internal/provider"
	"github.com/spf13/cobra"
)

// resolveCredentials resolves provider credentials from flags → env vars.
// Flag values take priority. Env vars are the fallback.
func resolveCredentials(cmd *cobra.Command, providerName string) (map[string]string, error) {
	schema, err := provider.GetComputeSchema(providerName)
	if err != nil {
		return nil, err
	}

	creds := make(map[string]string, len(schema.Fields))
	for _, f := range schema.Fields {
		// 1. Flag
		if cmd.Flags().Changed(f.Flag) {
			v, _ := cmd.Flags().GetString(f.Flag)
			creds[f.Key] = v
			continue
		}
		// 2. Env var
		if v := os.Getenv(f.EnvVar); v != "" {
			creds[f.Key] = v
			continue
		}
		// Leave empty — provider.Validate will catch required fields
	}
	return creds, nil
}

// resolveAppEnv reads NVOI_APP_NAME and NVOI_ENV from env vars.
func resolveAppEnv() (appName, env string, err error) {
	appName = os.Getenv("NVOI_APP_NAME")
	env = os.Getenv("NVOI_ENV")
	if appName == "" || env == "" {
		return "", "", fmt.Errorf("NVOI_APP_NAME and NVOI_ENV are required.\n  export NVOI_APP_NAME=dummy-rails\n  export NVOI_ENV=production")
	}
	return appName, env, nil
}

// resolveSSHKey reads the SSH private key from disk.
// Path from SSH_KEY_PATH env var, fallback to ~/.ssh/id_ed25519.
func resolveSSHKey() ([]byte, error) {
	keyPath := os.Getenv("SSH_KEY_PATH")
	if keyPath != "" {
		key, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, fmt.Errorf("read SSH key %s: %w", keyPath, err)
		}
		return key, nil
	}
	// Try common key paths
	home := os.Getenv("HOME")
	for _, name := range []string{"id_ed25519", "id_rsa"} {
		p := home + "/.ssh/" + name
		if key, err := os.ReadFile(p); err == nil {
			return key, nil
		}
	}
	return nil, fmt.Errorf("no SSH key found — set SSH_KEY_PATH or place a key at ~/.ssh/id_ed25519 or ~/.ssh/id_rsa")
}

// resolveBuildCredentials resolves build provider credentials from --builder-credentials flag → env vars.
// Flag values take priority over env vars.
func resolveBuildCredentials(cmd *cobra.Command, buildProviderName string) (map[string]string, error) {
	schema, err := provider.GetBuildSchema(buildProviderName)
	if err != nil {
		return nil, err
	}

	// Parse --builder-credentials flag (key=value pairs)
	flagCreds := make(map[string]string)
	if cmd.Flags().Changed("builder-credentials") {
		pairs, _ := cmd.Flags().GetStringArray("builder-credentials")
		for _, pair := range pairs {
			k, v, ok := strings.Cut(pair, "=")
			if !ok {
				return nil, fmt.Errorf("invalid builder credential %q — expected key=value", pair)
			}
			flagCreds[k] = v
		}
	}

	creds := make(map[string]string, len(schema.Fields))
	for _, f := range schema.Fields {
		// 1. Flag takes priority
		if v, ok := flagCreds[f.Key]; ok {
			creds[f.Key] = v
			continue
		}
		// 2. Env var fallback
		if v := os.Getenv(f.EnvVar); v != "" {
			creds[f.Key] = v
		}
	}
	return creds, nil
}

// resolveGitAuth resolves git credentials for cloning remote repos.
// Resolution order: signed URL (caller checks) → gh auth token → --git-token flag → GITHUB_TOKEN env.
// Returns (username, token). Both empty if nothing found — app/ enforces when needed.
func resolveGitAuth(cmd *cobra.Command) (string, string) {
	// 1. Signed URL — handled by app/ (it parses the source value)

	// 2. gh auth token
	out, err := exec.Command("gh", "auth", "token").Output()
	if err == nil {
		token := strings.TrimSpace(string(out))
		if token != "" {
			return "x-access-token", token
		}
	}

	// 3. --git-token flag
	if cmd.Flags().Changed("git-token") {
		token, _ := cmd.Flags().GetString("git-token")
		if token != "" {
			return "x-access-token", token
		}
	}

	// 4. GITHUB_TOKEN env var
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		return "x-access-token", token
	}

	return "", ""
}

// addProviderFlags adds --provider and credential flags for a compute provider.
func addProviderFlags(cmd *cobra.Command) {
	cmd.Flags().String("provider", "", "compute provider (hetzner)")
	cmd.Flags().String("token", "", "provider API token (overrides env var)")
	_ = cmd.MarkFlagRequired("provider")
}
