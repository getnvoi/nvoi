package reconcile

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/getnvoi/nvoi/internal/config"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/spf13/viper"
)

// collectSecretKeys gathers all secret keys that need to be read from viper.
// This includes:
//   - Bare names (no "=") → the key itself is read from env
//   - $VAR references on the right side of "=" → each referenced var is read from env
//
// This ensures EMAIL_HOST_USER=$BUGSINK_EMAIL_HOST_USER works without
// declaring BUGSINK_EMAIL_HOST_USER as a separate bare entry first.
func collectSecretKeys(cfg *config.AppConfig) []string {
	seen := map[string]bool{}
	for _, key := range cfg.Secrets {
		seen[key] = true
	}
	collect := func(refs []string) {
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
	for _, svc := range cfg.Services {
		collect(svc.Secrets)
	}
	for _, cron := range cfg.Crons {
		collect(cron.Secrets)
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Secrets reconciles the global k8s Secret, reads available secret values
// from viper, and returns them for downstream $VAR resolution.
// Keys not found in viper are skipped — they may be provided later by
// packages or storage. Completeness is validated at resolution time.
func Secrets(ctx context.Context, dc *config.DeployContext, live *config.LiveState, cfg *config.AppConfig, v *viper.Viper) (map[string]string, error) {
	allKeys := collectSecretKeys(cfg)
	secretValues := make(map[string]string, len(allKeys))

	// Read available values from viper — skip missing (may come from packages/storage)
	for _, key := range allKeys {
		if val := v.GetString(key); val != "" {
			secretValues[key] = val
		}
	}

	// Global secrets MUST be in viper — they have no other source
	for _, key := range cfg.Secrets {
		if secretValues[key] == "" {
			return nil, fmt.Errorf("secret %q listed in config but not found in environment", key)
		}
	}

	// Store global secrets in the shared k8s Secret
	for _, key := range cfg.Secrets {
		if err := app.SecretSet(ctx, app.SecretSetRequest{
			Cluster: dc.Cluster, Key: key, Value: secretValues[key],
		}); err != nil {
			return nil, err
		}
	}

	// Orphan removal for global secrets
	if live != nil {
		desired := toSet(cfg.Secrets)
		for _, key := range live.Secrets {
			if !desired[key] {
				if err := app.SecretDelete(ctx, app.SecretDeleteRequest{Cluster: dc.Cluster, Key: key}); err != nil {
					dc.Cluster.Log().Warning(fmt.Sprintf("orphan secret %s not removed: %s", key, err))
				}
			}
		}
	}

	return secretValues, nil
}
