package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/internal/packages/database"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/provider"
)

// ── Credential resolution ───────────────────────────────────────────────────
// os.Getenv lives here — the cmd/ boundary. Everything below receives resolved values.

func buildDeployContext(ctx context.Context, out app.Output, cfg *config.AppConfig) *config.DeployContext {
	source := credentialSource(ctx, cfg)

	computeCreds, _ := resolveProviderCreds(source, "compute", cfg.Providers.Compute)
	sshKey, _ := resolveSSHKey()
	dnsCreds, _ := resolveProviderCreds(source, "dns", cfg.Providers.DNS)
	storageCreds, _ := resolveProviderCreds(source, "storage", cfg.Providers.Storage)
	builderCreds, _ := resolveProviderCreds(source, "build", cfg.Providers.Build)
	gitUsername, gitToken := resolveGitAuth()
	dbCreds := resolveDatabaseCreds(cfg)

	return &config.DeployContext{
		Cluster: app.Cluster{
			AppName:     cfg.App,
			Env:         cfg.Env,
			Provider:    cfg.Providers.Compute,
			Credentials: computeCreds,
			SSHKey:      sshKey,
			Output:      out,
		},
		DNS:           app.ProviderRef{Name: cfg.Providers.DNS, Creds: dnsCreds},
		Storage:       app.ProviderRef{Name: cfg.Providers.Storage, Creds: storageCreds},
		Builder:       cfg.Providers.Build,
		BuildCreds:    builderCreds,
		GitUsername:   gitUsername,
		GitToken:      gitToken,
		DatabaseCreds: dbCreds,
	}
}

// credentialSource returns the single CredentialSource for this deploy.
// If a secrets provider is configured, its own credentials are bootstrapped
// from env vars, then all other provider credentials are fetched through it.
// If no secrets provider is configured, credentials come from env vars directly.
func credentialSource(ctx context.Context, cfg *config.AppConfig) provider.CredentialSource {
	if sp := cfg.Providers.Secrets; sp != nil {
		// Bootstrap: the secrets provider's own creds always come from env.
		spCreds, err := resolveProviderCreds(provider.EnvSource{}, "secrets", sp.Kind)
		if err == nil && len(spCreds) > 0 {
			secretsProv, err := provider.ResolveSecrets(sp.Kind, spCreds)
			if err == nil {
				return provider.SecretsSource{Ctx: ctx, Provider: secretsProv}
			}
		}
	}
	return provider.EnvSource{}
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

func resolveSSHKey() ([]byte, error) {
	keyPath := os.Getenv("SSH_KEY_PATH")
	if keyPath != "" {
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

func resolveDatabaseCreds(cfg *config.AppConfig) map[string]*config.DatabaseCredentials {
	if len(cfg.Database) == 0 {
		return nil
	}
	creds := make(map[string]*config.DatabaseCredentials, len(cfg.Database))
	for name, db := range cfg.Database {
		engine := database.EngineFor(db.Kind)
		userEnv, passEnv, dbEnv := engine.EnvVarNames()
		prefix := strings.ToUpper(name)
		creds[name] = &config.DatabaseCredentials{
			User:     os.Getenv(prefix + "_" + userEnv),
			Password: os.Getenv(prefix + "_" + passEnv),
			DBName:   os.Getenv(prefix + "_" + dbEnv),
		}
	}
	return creds
}
