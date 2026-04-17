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
//
// When a secrets backend is in use (`providers.secrets` set), strict mode
// applies: SSH_PRIVATE_KEY must be stored in the backend as a PEM blob.
// No disk fallback — a backend declares itself the single source of truth
// and silently reading `~/.ssh/id_*` would create a ghost dependency on
// the operator's home directory that nobody can audit.
//
// Under EnvSource (no backend), resolution order is the historical one:
//  1. SSH_PRIVATE_KEY (env, full PEM blob)
//  2. SSH_KEY_PATH (env, tilde-expanded) → read file
//  3. ~/.ssh/id_ed25519 or ~/.ssh/id_rsa on disk
func resolveSSHKey(source provider.CredentialSource) ([]byte, error) {
	if pem, _ := source.Get("SSH_PRIVATE_KEY"); pem != "" {
		return []byte(pem), nil
	}
	if _, strict := source.(provider.SecretsSource); strict {
		return nil, fmt.Errorf("secrets backend in use — SSH_PRIVATE_KEY must be stored in the backend (no disk fallback)")
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
