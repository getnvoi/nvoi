package reconcile

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/getnvoi/nvoi/internal/config"
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

// Secrets reads secret values from viper and returns them for downstream
// $VAR resolution. No k8s writes — secrets reach the cluster only via
// per-service k8s Secrets in the Services/Crons reconcilers.
func Secrets(_ context.Context, _ *config.DeployContext, _ *config.LiveState, cfg *config.AppConfig, v *viper.Viper) (map[string]string, error) {
	allKeys := collectSecretKeys(cfg)
	secretValues := make(map[string]string, len(allKeys))

	// Read available values from viper — skip missing (may come from packages/storage)
	for _, key := range allKeys {
		if val := v.GetString(key); val != "" {
			secretValues[key] = val
		}
	}

	// Global secrets MUST be in viper — unless ESO is managing them.
	// When a secrets provider is configured, ESO fetches app secrets
	// inside the cluster. They won't be in the local environment.
	if cfg.Providers.Secrets == "" {
		for _, key := range cfg.Secrets {
			if secretValues[key] == "" {
				return nil, fmt.Errorf("secret %q listed in config but not found in environment", key)
			}
		}
	}

	return secretValues, nil
}
