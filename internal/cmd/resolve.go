package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/getnvoi/nvoi/internal/provider"
	"github.com/spf13/cobra"
)

// ── App + Env ────────────────────────────────────────────────────────────────

// resolveAppEnv reads app name and env from flags → env vars.
// Flag takes priority. Env var is the fallback.
func resolveAppEnv(cmd *cobra.Command) (appName, env string, err error) {
	appName, _ = cmd.Flags().GetString("app-name")
	if appName == "" {
		appName = os.Getenv("NVOI_APP_NAME")
	}
	env, _ = cmd.Flags().GetString("env")
	if env == "" {
		env = os.Getenv("NVOI_ENV")
	}
	if appName == "" || env == "" {
		return "", "", fmt.Errorf("app name and env are required.\n  flags:    --app-name rails --env production\n  env vars: export NVOI_APP_NAME=rails NVOI_ENV=production")
	}
	return appName, env, nil
}

// ── Compute provider ─────────────────────────────────────────────────────────

// resolveComputeProvider reads --compute-provider flag → COMPUTE_PROVIDER env var.
func resolveComputeProvider(cmd *cobra.Command) (string, error) {
	name, _ := cmd.Flags().GetString("compute-provider")
	if name == "" {
		name = os.Getenv("COMPUTE_PROVIDER")
	}
	if name == "" {
		return "", fmt.Errorf("compute provider is required.\n  flag:    --compute-provider hetzner\n  env var: export COMPUTE_PROVIDER=hetzner")
	}
	return name, nil
}

// resolveComputeCredentials resolves compute provider credentials from flags → env vars.
// --compute-credentials KEY=VALUE pairs take priority. Per-provider env vars are the fallback.
func resolveComputeCredentials(cmd *cobra.Command, providerName string) (map[string]string, error) {
	schema, err := provider.GetComputeSchema(providerName)
	if err != nil {
		return nil, err
	}

	// Parse --compute-credentials flag (key=value pairs)
	flagCreds := make(map[string]string)
	if cmd.Flags().Changed("compute-credentials") {
		pairs, _ := cmd.Flags().GetStringArray("compute-credentials")
		for _, pair := range pairs {
			k, v, ok := strings.Cut(pair, "=")
			if !ok {
				return nil, fmt.Errorf("invalid compute credential %q — expected KEY=VALUE", pair)
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
			continue
		}
		// Leave empty — provider.Validate will catch required fields
	}
	return creds, nil
}

// ── Build provider ───────────────────────────────────────────────────────────

// resolveBuildProvider reads --build-provider flag → BUILD_PROVIDER env var.
func resolveBuildProvider(cmd *cobra.Command) (string, error) {
	name, _ := cmd.Flags().GetString("build-provider")
	if name == "" {
		name = os.Getenv("BUILD_PROVIDER")
	}
	if name == "" {
		return "", fmt.Errorf("build provider is required.\n  flag:    --build-provider local\n  env var: export BUILD_PROVIDER=local")
	}
	return name, nil
}

// resolveBuildCredentials resolves build provider credentials from --build-credentials flag → env vars.
func resolveBuildCredentials(cmd *cobra.Command, buildProviderName string) (map[string]string, error) {
	schema, err := provider.GetBuildSchema(buildProviderName)
	if err != nil {
		return nil, err
	}

	// Parse --build-credentials flag (key=value pairs)
	flagCreds := make(map[string]string)
	if cmd.Flags().Changed("build-credentials") {
		pairs, _ := cmd.Flags().GetStringArray("build-credentials")
		for _, pair := range pairs {
			k, v, ok := strings.Cut(pair, "=")
			if !ok {
				return nil, fmt.Errorf("invalid build credential %q — expected KEY=VALUE", pair)
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

// ── DNS provider ─────────────────────────────────────────────────────────────

// resolveDNSProvider reads --dns-provider flag → DNS_PROVIDER env var.
func resolveDNSProvider(cmd *cobra.Command) (string, error) {
	name, _ := cmd.Flags().GetString("dns-provider")
	if name == "" {
		name = os.Getenv("DNS_PROVIDER")
	}
	if name == "" {
		return "", fmt.Errorf("DNS provider is required.\n  flag:    --dns-provider cloudflare\n  env var: export DNS_PROVIDER=cloudflare")
	}
	return name, nil
}

// ── Storage provider ─────────────────────────────────────────────────────────

// resolveStorageProvider reads --storage-provider flag → STORAGE_PROVIDER env var.
func resolveStorageProvider(cmd *cobra.Command) (string, error) {
	name, _ := cmd.Flags().GetString("storage-provider")
	if name == "" {
		name = os.Getenv("STORAGE_PROVIDER")
	}
	if name == "" {
		return "", fmt.Errorf("storage provider is required.\n  flag:    --storage-provider cloudflare\n  env var: export STORAGE_PROVIDER=cloudflare")
	}
	return name, nil
}

// ── SSH key ──────────────────────────────────────────────────────────────────

// resolveSSHKey reads the SSH private key from disk.
// Path from SSH_KEY_PATH env var, fallback to ~/.ssh/id_ed25519 then ~/.ssh/id_rsa.
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

// ── Git auth ─────────────────────────────────────────────────────────────────

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

// ── Flag helpers ─────────────────────────────────────────────────────────────

// addComputeProviderFlags adds --compute-provider and --compute-credentials to a command.
func addComputeProviderFlags(cmd *cobra.Command) {
	cmd.Flags().String("compute-provider", "", "compute provider (hetzner)")
	cmd.Flags().StringArray("compute-credentials", nil, "compute provider credentials (KEY=VALUE)")
}

// addBuildProviderFlags adds --build-provider and --build-credentials to a command.
func addBuildProviderFlags(cmd *cobra.Command) {
	cmd.Flags().String("build-provider", "", "build provider (local, daytona, github)")
	cmd.Flags().StringArray("build-credentials", nil, "build provider credentials (KEY=VALUE)")
}

// addDNSProviderFlags adds --dns-provider to a command.
func addDNSProviderFlags(cmd *cobra.Command) {
	cmd.Flags().String("dns-provider", "", "DNS provider (cloudflare)")
}

// addStorageProviderFlags adds --storage-provider to a command.
func addStorageProviderFlags(cmd *cobra.Command) {
	cmd.Flags().String("storage-provider", "", "storage provider (cloudflare)")
}

// addAppFlags adds --app-name and --env to a command.
func addAppFlags(cmd *cobra.Command) {
	cmd.Flags().String("app-name", "", "application name (env: NVOI_APP_NAME)")
	cmd.Flags().String("env", "", "environment (env: NVOI_ENV)")
}
