package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/getnvoi/nvoi/internal/config"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/provider"
)

// ── Credential resolution ───────────────────────────────────────────────────
// os.Getenv lives here — the cmd/ boundary. Everything below receives resolved values.

func buildDeployContext(ctx context.Context, out app.Output, cfg *config.AppConfig) (*config.DeployContext, error) {
	source, err := credentialSource(ctx, cfg)
	if err != nil {
		return nil, err
	}

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

// credentialSource returns the single CredentialSource every downstream
// resolver reads through. Two modes, binary switch:
//
//   - providers.secrets unset → EnvSource{}. Every credential comes from
//     the operator's env / .env file. Same behavior as local-first nvoi.
//
//   - providers.secrets.kind set → SecretsSource. The backend's own
//     credentials are bootstrapped from env (the one escape hatch),
//     then every downstream credential (compute / DNS / storage / SSH
//     key / service $VAR expansion) is fetched from the backend at
//     deploy time. Misconfigured backend → hard error at startup, not
//     a deferred failure mid-deploy.
func credentialSource(ctx context.Context, cfg *config.AppConfig) (provider.CredentialSource, error) {
	sp := cfg.Providers.Secrets
	if sp == nil || sp.Kind == "" {
		return provider.EnvSource{}, nil
	}
	// Bootstrap: the secrets backend's own creds always come from env.
	// Without this escape hatch there'd be no way to authenticate to
	// the backend that holds everything else.
	spCreds, err := resolveProviderCreds(provider.EnvSource{}, "secrets", sp.Kind)
	if err != nil {
		return nil, fmt.Errorf("secrets backend %q: %w", sp.Kind, err)
	}
	prov, err := provider.ResolveSecrets(sp.Kind, spCreds)
	if err != nil {
		return nil, fmt.Errorf("secrets backend %q: %w", sp.Kind, err)
	}
	if err := prov.ValidateCredentials(ctx); err != nil {
		return nil, fmt.Errorf("secrets backend %q: %w", sp.Kind, err)
	}
	return provider.SecretsSource{Ctx: ctx, Provider: prov}, nil
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
//  1. SSH_PRIVATE_KEY (from source — env or secrets backend)
//  2. SSH_KEY_PATH (from source, tilde expanded) → read file
//  3. ~/.ssh/id_ed25519 or ~/.ssh/id_rsa on disk
//
// Step 3 is a local-filesystem fallback — genuinely absent from a
// secrets backend by design (backends don't store files). Safe because
// the default private-key paths are the same convention every other
// SSH-using tool follows.
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
