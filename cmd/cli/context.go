package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/getnvoi/nvoi/internal/config"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/provider"
)

// ── Credential resolution ───────────────────────────────────────────────────
// os.Getenv lives here — the cmd/ boundary. Everything below receives resolved values.

func buildDeployContext(out app.Output, cfg *config.AppConfig) (*config.DeployContext, error) {
	source := provider.EnvSource{}

	computeCreds, _ := resolveProviderCreds(source, "compute", cfg.Providers.Compute)
	sshKey, _ := resolveSSHKey(source)
	dnsCreds, _ := resolveProviderCreds(source, "dns", cfg.Providers.DNS)
	storageCreds, _ := resolveProviderCreds(source, "storage", cfg.Providers.Storage)

	return &config.DeployContext{
		Cluster: app.Cluster{
			AppName:     cfg.App,
			Env:         cfg.Env,
			Provider:    cfg.Providers.Compute,
			Credentials: computeCreds,
			SSHKey:      sshKey,
			Output:      out,
		},
		DNS:     app.ProviderRef{Name: cfg.Providers.DNS, Creds: dnsCreds},
		Storage: app.ProviderRef{Name: cfg.Providers.Storage, Creds: storageCreds},
		Creds:   source,
	}, nil
}

func resolveProviderCreds(source provider.CredentialSource, kind, name string) (map[string]string, error) {
	if name == "" {
		return nil, nil
	}
	schema, err := provider.GetSchema(kind, name)
	if err != nil {
		return nil, err
	}
	return provider.ResolveFrom(schema, source)
}

// resolveSSHKey reads the SSH private key for master/worker provisioning.
// Resolution order:
//  1. SSH_PRIVATE_KEY (env — full PEM blob)
//  2. SSH_KEY_PATH (env, tilde expanded)
//  3. ~/.ssh/id_ed25519 or ~/.ssh/id_rsa on disk
func resolveSSHKey(source provider.CredentialSource) ([]byte, error) {
	if pem, _ := source.Get("SSH_PRIVATE_KEY"); pem != "" {
		return []byte(pem), nil
	}
	if keyPath, _ := source.Get("SSH_KEY_PATH"); keyPath != "" {
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
	return nil, fmt.Errorf("no SSH key found — set SSH_PRIVATE_KEY, SSH_KEY_PATH, or place ~/.ssh/id_ed25519")
}
