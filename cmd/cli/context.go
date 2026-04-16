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

func buildDeployContext(ctx context.Context, out app.Output, cfg *config.AppConfig) (*config.DeployContext, error) {
	source, err := credentialSource(ctx, cfg)
	if err != nil {
		return nil, err
	}

	computeCreds, _ := resolveProviderCreds(source, "compute", cfg.Providers.Compute)
	sshKey, _ := resolveSSHKey(source)
	dnsCreds, _ := resolveProviderCreds(source, "dns", cfg.Providers.DNS)
	storageCreds, _ := resolveProviderCreds(source, "storage", cfg.Providers.Storage)
	builderCreds, _ := resolveProviderCreds(source, "build", cfg.Providers.Build)
	gitUsername, gitToken := resolveGitAuth(source)
	dbCreds := resolveDatabaseCreds(source, cfg)

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
		Creds:         source,
	}, nil
}

// credentialSource returns the single CredentialSource for this deploy.
// If a secrets provider is configured, its own credentials are bootstrapped
// from env vars, then ALL other credentials are fetched through it.
// No silent fallback: misconfigured secrets provider is a hard error —
// otherwise a bad token silently degrades to env lookup and fails mysteriously
// much later.
// If no secrets provider is configured, credentials come from env vars directly.
func credentialSource(ctx context.Context, cfg *config.AppConfig) (provider.CredentialSource, error) {
	sp := cfg.Providers.Secrets
	if sp == "" {
		return provider.EnvSource{}, nil
	}
	spCreds, err := resolveProviderCreds(provider.EnvSource{}, "secrets", sp)
	if err != nil {
		return nil, fmt.Errorf("secrets provider %q: resolve bootstrap credentials: %w", sp, err)
	}
	if len(spCreds) == 0 {
		return nil, fmt.Errorf("secrets provider %q: no bootstrap credentials found (check env vars)", sp)
	}
	secretsProv, err := provider.ResolveSecrets(sp, spCreds)
	if err != nil {
		return nil, fmt.Errorf("secrets provider %q: %w", sp, err)
	}
	return provider.SecretsSource{Ctx: ctx, Provider: secretsProv}, nil
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
// Strict mode (a secrets provider is configured): SSH_PRIVATE_KEY must be present
// in the provider — no filesystem fallback, no SSH_KEY_PATH indirection. The
// provider is THE source.
//
// Env mode (no secrets provider): full resolution —
//  1. SSH_PRIVATE_KEY (env)
//  2. SSH_KEY_PATH (env, tilde expanded)
//  3. ~/.ssh/id_ed25519 or ~/.ssh/id_rsa on disk
func resolveSSHKey(source provider.CredentialSource) ([]byte, error) {
	if pem, _ := source.Get("SSH_PRIVATE_KEY"); pem != "" {
		return []byte(pem), nil
	}
	if _, strict := source.(provider.SecretsSource); strict {
		return nil, fmt.Errorf("secrets provider enabled but SSH_PRIVATE_KEY missing — no disk fallback in strict mode")
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

// resolveGitAuth returns the (username, token) pair for git operations.
//
// Strict mode (a secrets provider is configured): GITHUB_TOKEN must come from
// the provider — no `gh auth token` subprocess. If the provider doesn't have
// it, returns ("", "") and callers that need auth fail downstream.
//
// Env mode (no secrets provider): GITHUB_TOKEN from env → `gh auth token` subprocess.
func resolveGitAuth(source provider.CredentialSource) (string, string) {
	if token, _ := source.Get("GITHUB_TOKEN"); token != "" {
		return "x-access-token", token
	}
	if _, strict := source.(provider.SecretsSource); strict {
		return "", ""
	}
	if out, err := exec.Command("gh", "auth", "token").Output(); err == nil {
		if token := strings.TrimSpace(string(out)); token != "" {
			return "x-access-token", token
		}
	}
	return "", ""
}

func resolveDatabaseCreds(source provider.CredentialSource, cfg *config.AppConfig) map[string]*config.DatabaseCredentials {
	if len(cfg.Database) == 0 {
		return nil
	}
	creds := make(map[string]*config.DatabaseCredentials, len(cfg.Database))
	for name, db := range cfg.Database {
		engine := database.EngineFor(db.Kind)
		userEnv, passEnv, dbEnv := engine.EnvVarNames()
		prefix := strings.ToUpper(name)
		user, _ := source.Get(prefix + "_" + userEnv)
		pass, _ := source.Get(prefix + "_" + passEnv)
		dbName, _ := source.Get(prefix + "_" + dbEnv)
		creds[name] = &config.DatabaseCredentials{
			User:     user,
			Password: pass,
			DBName:   dbName,
		}
	}
	return creds
}
