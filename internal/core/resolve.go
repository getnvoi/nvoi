package core

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/getnvoi/nvoi/pkg/provider"
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
	env, _ = cmd.Flags().GetString("environment")
	if env == "" {
		env = os.Getenv("NVOI_ENV")
	}
	if appName == "" || env == "" {
		return "", "", fmt.Errorf("app name and env are required.\n  flags:    --app-name rails --environment production\n  env vars: export NVOI_APP_NAME=rails NVOI_ENV=production")
	}
	return appName, env, nil
}

// ── Generic credential resolution ───────────────────────────────────────────

// resolveCredentials resolves provider credentials from flags → env vars.
// flagName is the --xxx-credentials flag (empty if the provider has no credential flag).
// Schema fields with a Flag value are also checked as direct command flags (e.g. --zone).
func resolveCredentials(cmd *cobra.Command, schema provider.CredentialSchema, flagName string) (map[string]string, error) {
	// Parse key=value pairs from the credentials flag
	flagCreds := make(map[string]string)
	if flagName != "" && cmd.Flags().Changed(flagName) {
		pairs, _ := cmd.Flags().GetStringArray(flagName)
		for _, pair := range pairs {
			k, v, ok := strings.Cut(pair, "=")
			if !ok {
				return nil, fmt.Errorf("invalid credential %q — expected KEY=VALUE", pair)
			}
			flagCreds[k] = v
		}
	}

	creds := make(map[string]string, len(schema.Fields))
	for _, f := range schema.Fields {
		// 1. Key=value credential flag takes priority
		if v, ok := flagCreds[f.Key]; ok {
			creds[f.Key] = v
			continue
		}
		// 2. Direct command flag (e.g. --zone for the "zone" field)
		if f.Flag != "" {
			if v, _ := cmd.Flags().GetString(f.Flag); v != "" {
				creds[f.Key] = v
				continue
			}
		}
		// 3. Env var fallback
		if v := os.Getenv(f.EnvVar); v != "" {
			creds[f.Key] = v
		}
	}
	return creds, nil
}

// ── Compute provider ─────────────────────────────────────────────────────────

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

func resolveComputeCredentials(cmd *cobra.Command, providerName string) (map[string]string, error) {
	return resolveProviderCredentials(cmd, "compute", providerName, "compute-credentials")
}

// ── Build provider ───────────────────────────────────────────────────────────

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

func resolveBuildCredentials(cmd *cobra.Command, buildProviderName string) (map[string]string, error) {
	return resolveProviderCredentials(cmd, "build", buildProviderName, "build-credentials")
}

// ── DNS provider ─────────────────────────────────────────────────────────────

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

func resolveDNSCredentials(cmd *cobra.Command, providerName string) (map[string]string, error) {
	return resolveProviderCredentials(cmd, "dns", providerName, "")
}

// ── Storage provider ─────────────────────────────────────────────────────────

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

func resolveStorageCredentials(cmd *cobra.Command, providerName string) (map[string]string, error) {
	return resolveProviderCredentials(cmd, "storage", providerName, "")
}

// resolveProviderCredentials resolves credentials for any provider kind using the unified schema lookup.
func resolveProviderCredentials(cmd *cobra.Command, kind, name, flagName string) (map[string]string, error) {
	schema, err := provider.GetSchema(kind, name)
	if err != nil {
		return nil, err
	}
	return resolveCredentials(cmd, schema, flagName)
}

// ── SSH key ──────────────────────────────────────────────────────────────────

func resolveSSHKey() ([]byte, error) {
	keyPath := os.Getenv("SSH_KEY_PATH")
	if keyPath != "" {
		keyPath = expandHome(keyPath)
		key, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, fmt.Errorf("read SSH key %s: %w", keyPath, err)
		}
		return key, nil
	}
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

func resolveGitAuth(cmd *cobra.Command) (string, string) {
	out, err := exec.Command("gh", "auth", "token").Output()
	if err == nil {
		token := strings.TrimSpace(string(out))
		if token != "" {
			return "x-access-token", token
		}
	}
	if cmd.Flags().Changed("git-token") {
		token, _ := cmd.Flags().GetString("git-token")
		if token != "" {
			return "x-access-token", token
		}
	}
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		return "x-access-token", token
	}
	return "", ""
}

// expandHome replaces a leading ~ with $HOME.
func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		if home := os.Getenv("HOME"); home != "" {
			return home + path[1:]
		}
	}
	return path
}

// ── Flag helpers ─────────────────────────────────────────────────────────────

func addComputeProviderFlags(cmd *cobra.Command) {
	cmd.Flags().String("compute-provider", "", "compute provider (hetzner)")
	cmd.Flags().StringArray("compute-credentials", nil, "compute provider credentials (KEY=VALUE)")
}

func addBuildProviderFlags(cmd *cobra.Command) {
	cmd.Flags().String("build-provider", "", "build provider (local, daytona, github)")
	cmd.Flags().StringArray("build-credentials", nil, "build provider credentials (KEY=VALUE)")
}

func addDNSProviderFlags(cmd *cobra.Command) {
	cmd.Flags().String("dns-provider", "", "DNS provider (cloudflare)")
}

func addStorageProviderFlags(cmd *cobra.Command) {
	cmd.Flags().String("storage-provider", "", "storage provider (cloudflare)")
}

func addAppFlags(cmd *cobra.Command) {
	cmd.Flags().String("app-name", "", "application name (env: NVOI_APP_NAME)")
	cmd.Flags().String("environment", "", "environment (env: NVOI_ENV)")
}
