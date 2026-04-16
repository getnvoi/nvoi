package main

import (
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

func buildDeployContext(out app.Output, cfg *config.AppConfig) *config.DeployContext {
	computeCreds, _ := resolveProviderCreds("compute", cfg.Providers.Compute)
	sshKey, _ := resolveSSHKey()
	dnsCreds, _ := resolveProviderCreds("dns", cfg.Providers.DNS)
	storageCreds, _ := resolveProviderCreds("storage", cfg.Providers.Storage)
	builderCreds, _ := resolveProviderCreds("build", cfg.Providers.Build)
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
