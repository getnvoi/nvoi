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

// collectSecretKeys gathers all bare secret keys that need to be read
// from viper. Bare = no "=" in the entry. Entries with "=" always have $
// (enforced by validation) and resolve from sources later, not from viper.
func collectSecretKeys(cfg *config.AppConfig) []string {
	seen := map[string]bool{}
	for _, key := range cfg.Secrets {
		seen[key] = true
	}
	for _, svc := range cfg.Services {
		for _, ref := range svc.Secrets {
			if !strings.Contains(ref, "=") {
				seen[ref] = true
			}
		}
	}
	for _, cron := range cfg.Crons {
		for _, ref := range cron.Secrets {
			if !strings.Contains(ref, "=") {
				seen[ref] = true
			}
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Secrets reconciles the global k8s Secret, reads all declared secret values
// from viper, and returns them for downstream $VAR resolution.
func Secrets(ctx context.Context, dc *config.DeployContext, live *config.LiveState, cfg *config.AppConfig, v *viper.Viper) (map[string]string, error) {
	allKeys := collectSecretKeys(cfg)
	secretValues := make(map[string]string, len(allKeys))

	// Read all values from viper
	for _, key := range allKeys {
		val := v.GetString(key)
		if val == "" {
			return nil, fmt.Errorf("secret %q listed in config but not found in environment", key)
		}
		secretValues[key] = val
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
