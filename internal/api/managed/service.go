// Package managed defines the interface and registry for managed infrastructure
// services (databases, caches, search engines). Each managed service knows how
// to produce a config.Service spec and generate credentials. The Expand function
// transforms a public config (with managed: fields) into an internal config
// where managed services are replaced with fully populated service entries.
package managed

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/pkg/utils"

	"github.com/getnvoi/nvoi/internal/api/config"
)

// ManagedService defines a managed infrastructure service.
// One file per implementation. Registration via init().
type ManagedService interface {
	// Kind returns the identifier used in config YAML (e.g. "postgres").
	Kind() string

	// Spec returns a fully populated Service entry for the config.
	// The name is the service name from the config (e.g. "db").
	Spec(name string) config.Service

	// Credentials generates new credentials for this service.
	// Called once on first provision, stored encrypted in DB forever.
	// Keys are suffixes: "HOST", "PORT", "USER", "PASSWORD", "URL", etc.
	Credentials(name string) map[string]string

	// EnvPrefix returns the prefix for injected env vars (e.g. "DATABASE").
	// Credentials are injected as {PREFIX}_{NAME}_{KEY} (e.g. DATABASE_DB_HOST).
	EnvPrefix() string

	// InternalSecrets returns namespaced secret key → value pairs for the spec's
	// own secrets. Name is included to avoid collisions across multiple instances.
	// e.g. postgres "db" → {"POSTGRES_PASSWORD_DB": "<generated>"}.
	// The spec's Secrets list uses aliasing (ENV=SECRET_KEY) to map these back
	// to the env var the container expects.
	InternalSecrets(name string, creds map[string]string) map[string]string
}

// ── Registry ─────────────────────────────────────────────────────────────────

var registry = map[string]ManagedService{}

func Register(ms ManagedService) {
	registry[ms.Kind()] = ms
}

func Get(kind string) (ManagedService, bool) {
	ms, ok := registry[kind]
	return ms, ok
}

// Registered returns all registered managed service kinds.
func Registered() []string {
	kinds := make([]string, 0, len(registry))
	for k := range registry {
		kinds = append(kinds, k)
	}
	return kinds
}

// ── Expand ───────────────────────────────────────────────────────────────────

// Expand transforms a public config into an internal config by replacing
// managed services with fully populated service entries and injecting
// credentials into dependent services.
//
// storedCreds holds previously generated credentials keyed by service name.
// If a managed service's credentials are not in storedCreds, new ones are
// generated and added to the returned newCreds map.
//
// The returned config is a modified copy — the original is not mutated.
func Expand(cfg *config.Config, storedCreds map[string]map[string]string) (expanded *config.Config, newCreds map[string]map[string]string, err error) {
	// Deep copy services + volumes maps.
	services := make(map[string]config.Service, len(cfg.Services))
	for k, v := range cfg.Services {
		services[k] = v
	}
	volumes := make(map[string]config.Volume, len(cfg.Volumes))
	for k, v := range cfg.Volumes {
		volumes[k] = v
	}

	// Default server for auto-generated volumes (first alphabetically).
	defaultServer := ""
	for _, k := range utils.SortedKeys(cfg.Servers) {
		defaultServer = k
		break
	}

	newCreds = map[string]map[string]string{}

	// Phase 1: Replace managed services with real specs + resolve credentials.
	managedCreds := map[string]map[string]string{} // name → {HOST: ..., PORT: ..., ...}
	managedPrefixes := map[string]string{}         // name → "DATABASE"

	for name, svc := range services {
		if svc.Managed == "" {
			continue
		}
		ms, ok := Get(svc.Managed)
		if !ok {
			return nil, nil, fmt.Errorf("services.%s.managed: %q is not a registered managed service", name, svc.Managed)
		}

		// Replace with real spec.
		spec := ms.Spec(name)
		services[name] = spec

		// Auto-add volumes required by the managed service spec.
		for _, mount := range spec.Volumes {
			volName, _, ok := strings.Cut(mount, ":")
			if !ok {
				continue
			}
			if _, exists := volumes[volName]; !exists {
				volumes[volName] = config.Volume{Size: 10, Server: defaultServer}
			}
		}

		// Resolve credentials.
		creds, ok := storedCreds[name]
		if !ok {
			creds = ms.Credentials(name)
			newCreds[name] = creds
		}
		managedCreds[name] = creds
		managedPrefixes[name] = ms.EnvPrefix()
	}

	// Phase 2: Expand uses refs on consuming services.
	for name, svc := range services {
		if len(svc.Uses) == 0 {
			continue
		}
		var extraSecrets []string
		for _, ref := range svc.Uses {
			creds, ok := managedCreds[ref]
			if !ok {
				return nil, nil, fmt.Errorf("services.%s.uses: %q has no credentials", name, ref)
			}
			prefix := managedPrefixes[ref]
			for key := range creds {
				secretKey := envKey(prefix, ref, key)
				extraSecrets = append(extraSecrets, secretKey)
			}
		}
		expanded := svc
		expanded.Secrets = append(append([]string{}, svc.Secrets...), extraSecrets...)
		expanded.Uses = nil // consumed — don't carry forward
		services[name] = expanded
	}

	// Build expanded config.
	result := *cfg
	result.Services = services
	result.Volumes = volumes
	return &result, newCreds, nil
}

// CredentialSecrets returns the flat map of secret key → value for all managed
// service credentials. Used to inject into the env/secrets before Plan().
func CredentialSecrets(managedCreds map[string]map[string]string, cfg *config.Config) map[string]string {
	secrets := map[string]string{}
	for name, creds := range managedCreds {
		svc, ok := cfg.Services[name]
		if !ok {
			continue
		}
		ms, ok := Get(svc.Managed)
		if !ok {
			continue
		}
		prefix := ms.EnvPrefix()
		for key, val := range creds {
			secrets[envKey(prefix, name, key)] = val
		}
		// Internal secrets: namespaced keys for the spec's own secrets.
		// e.g. POSTGRES_PASSWORD_DB = generated PASSWORD value.
		for k, v := range ms.InternalSecrets(name, creds) {
			secrets[k] = v
		}
	}
	return secrets
}

// envKey builds the secret key: PREFIX_NAME_SUFFIX (e.g. DATABASE_DB_HOST).
func envKey(prefix, name, suffix string) string {
	n := strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
	return prefix + "_" + n + "_" + suffix
}

// ── Helpers ──────────────────────────────────────────────────────────────────

// RandomHex generates a cryptographically random hex string of n bytes.
func RandomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
