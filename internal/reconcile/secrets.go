package reconcile

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/getnvoi/nvoi/internal/config"
)

// collectSecretKeys gathers all secret keys that need to be resolved.
// This includes:
//   - Bare names (no "=") → the key itself is read from the source
//   - $VAR references on the right side of "=" → each referenced var is read from the source
//   - $VAR references inside any field that participates in resolveRef
//     against the sources map (env, databases.X.credentials.*)
//
// Every field whose value flows through resolveRef(val, sources) MUST
// appear here — otherwise the reference can't be resolved at deploy
// time when providers.secrets is set (the backend is the source, and
// it's only queried for keys we collect here). The CLI side mirrors
// this list in cmd/cli/database.go::collectCommandSecrets; keep them
// in lockstep, or the laptop and the runner drift on what counts.
//
// This ensures EMAIL_HOST_USER=$BUGSINK_EMAIL_HOST_USER works without
// declaring BUGSINK_EMAIL_HOST_USER as a separate bare entry first.
func collectSecretKeys(cfg *config.AppConfig) []string {
	seen := map[string]bool{}
	for _, key := range cfg.Secrets {
		seen[key] = true
	}
	collectRefs := func(refs []string) {
		for _, ref := range refs {
			if !strings.Contains(ref, "=") {
				seen[ref] = true
			} else {
				// Extract $VAR references from the value side
				for _, varName := range extractVarRefs(ref) {
					seen[varName] = true
				}
			}
		}
	}
	collectVars := func(raw string) {
		for _, varName := range extractVarRefs(raw) {
			seen[varName] = true
		}
	}
	for _, svc := range cfg.Services {
		collectRefs(svc.Secrets)
		// env: entries resolve $VAR refs against the same sources map
		// (services.go::Services calls resolveEntry → resolveRef). Without
		// pre-collection, `env: [HOST=$EMAIL_HOST]` would never fetch
		// EMAIL_HOST from the secrets backend.
		for _, entry := range svc.Env {
			collectVars(entry)
		}
	}
	for _, cron := range cfg.Crons {
		collectRefs(cron.Secrets)
		for _, entry := range cron.Env {
			collectVars(entry)
		}
	}
	// databases.X.credentials.{user,password,database} also resolve via
	// resolveRef in databaseRequest. SaaS engines have no credentials
	// block — nil check skips them.
	for _, db := range cfg.Databases {
		if db.Credentials == nil {
			continue
		}
		collectVars(db.Credentials.User)
		collectVars(db.Credentials.Password)
		collectVars(db.Credentials.Database)
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Secrets resolves secret values from dc.Creds and returns them for downstream
// $VAR resolution. No k8s writes — secrets reach the cluster only via
// per-service k8s Secrets in the Services/Crons reconcilers.
func Secrets(_ context.Context, dc *config.DeployContext, cfg *config.AppConfig) (map[string]string, error) {
	allKeys := collectSecretKeys(cfg)
	secretValues := make(map[string]string, len(allKeys))

	// Read available values from the source — skip missing (may come from packages/storage)
	for _, key := range allKeys {
		val, err := dc.Creds.Get(key)
		if err != nil {
			return nil, fmt.Errorf("secret %q: %w", key, err)
		}
		if val != "" {
			secretValues[key] = val
		}
	}

	// Global secrets MUST be present — they have no other source
	for _, key := range cfg.Secrets {
		if secretValues[key] == "" {
			return nil, fmt.Errorf("secret %q listed in config but not found", key)
		}
	}

	return secretValues, nil
}
