package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/internal/packages/database"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/provider"
)

// CredentialSource returns the single CredentialSource for a config.
// If a secrets provider is configured, its own credentials are bootstrapped
// from env vars (via EnvSource — the designed abstraction), then all other
// credentials are fetched through it.
// If no secrets provider is configured, credentials come from env vars directly.
// Exported — used by both the agent and the CLI client (to resolve compute creds for SSH).
func CredentialSource(ctx context.Context, cfg *config.AppConfig) (provider.CredentialSource, error) {
	sp := cfg.Providers.Secrets
	if sp == "" {
		return provider.EnvSource{}, nil
	}
	// Secrets provider configured — must succeed. No silent fallback.
	spCreds, err := ResolveProviderCreds(provider.EnvSource{}, "secrets", sp)
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

// BuildDeployContext resolves all credentials and builds a DeployContext.
// SSH key and git auth come from AgentOpts (resolved at startup by cmd/).
// Provider credentials come from CredentialSource (EnvSource or SecretsSource).
// KubeClient comes from AgentOpts (created at agent startup, direct localhost:6443).
// Called per command — provider credentials are resolved fresh each time.
func BuildDeployContext(ctx context.Context, out app.Output, cfg *config.AppConfig, opts AgentOpts) (*config.DeployContext, error) {
	source, err := CredentialSource(ctx, cfg)
	if err != nil {
		return nil, err
	}

	computeCreds, _ := ResolveProviderCreds(source, "compute", cfg.Providers.Compute)
	dnsCreds, _ := ResolveProviderCreds(source, "dns", cfg.Providers.DNS)
	storageCreds, _ := ResolveProviderCreds(source, "storage", cfg.Providers.Storage)
	builderCreds, _ := ResolveProviderCreds(source, "build", cfg.Providers.Build)
	dbCreds := resolveDatabaseCreds(source, cfg)

	return &config.DeployContext{
		Cluster: app.Cluster{
			AppName:     cfg.App,
			Env:         cfg.Env,
			Provider:    cfg.Providers.Compute,
			Credentials: computeCreds,
			SSHKey:      opts.SSHKey,
			Kube:        opts.Kube,
		},
		Output:        out,
		DNS:           app.ProviderRef{Name: cfg.Providers.DNS, Creds: dnsCreds},
		Storage:       app.ProviderRef{Name: cfg.Providers.Storage, Creds: storageCreds},
		Builder:       cfg.Providers.Build,
		BuildCreds:    builderCreds,
		GitUsername:   opts.GitUsername,
		GitToken:      opts.GitToken,
		DatabaseCreds: dbCreds,
		Creds:         source,
	}, nil
}

// ResolveProviderCreds resolves credentials for a provider kind+name from any source.
// Exported — used by both the agent and the CLI client.
func ResolveProviderCreds(source provider.CredentialSource, kind, name string) (map[string]string, error) {
	if name == "" {
		return nil, nil
	}
	schema, err := provider.GetSchema(kind, name)
	if err != nil {
		return nil, err
	}
	return provider.ResolveFrom(schema, source)
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
