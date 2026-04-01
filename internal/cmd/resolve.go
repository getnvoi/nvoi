package cmd

import (
	"fmt"
	"os"

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
	if keyPath == "" {
		keyPath = os.ExpandEnv("$HOME/.ssh/id_ed25519")
	}
	key, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("read SSH key %s: %w", keyPath, err)
	}
	return key, nil
}

// addProviderFlags adds --provider and credential flags for a compute provider.
func addProviderFlags(cmd *cobra.Command) {
	cmd.Flags().String("provider", "", "compute provider (hetzner)")
	// Provider-specific credential flags are generic — each provider
	// declares what it needs, but we add common ones here.
	cmd.Flags().String("token", "", "provider API token (overrides env var)")
	_ = cmd.MarkFlagRequired("provider")
}
