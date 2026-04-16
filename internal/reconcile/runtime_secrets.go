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

// Secrets resolves secret values from dc.Creds and returns them for downstream
// $VAR resolution. No k8s writes — secrets reach the cluster only via
// per-service k8s Secrets in the Services/Crons reconcilers.
func Secrets(_ context.Context, dc *config.DeployContext, _ *config.LiveState, cfg *config.AppConfig) (map[string]string, error) {
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
