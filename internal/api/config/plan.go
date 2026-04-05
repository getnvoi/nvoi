package config

import (
	"fmt"
	"sort"
	"strings"
)

// StepKind identifies the pkg/core/ function to call.
type StepKind string

const (
	StepComputeSet StepKind = "instance.set"
	StepVolumeSet  StepKind = "volume.set"
	StepBuild      StepKind = "build"
	StepSecretSet  StepKind = "secret.set"
	StepStorageSet StepKind = "storage.set"
	StepServiceSet StepKind = "service.set"
	StepDNSSet     StepKind = "dns.set"
)

// Step is one action in the deploy plan.
// Maps 1:1 to a pkg/core/ function call.
type Step struct {
	Kind   StepKind       `json:"kind"`
	Name   string         `json:"name"`
	Params map[string]any `json:"params,omitempty"`
}

// Plan generates the ordered deploy sequence from a config + env.
// The sequence mirrors bin/deploy-* scripts:
//
//  1. Compute (servers)
//  2. Volumes
//  3. Build
//  4. Secrets
//  5. Storage
//  6. Services
//  7. DNS
//
// env is the parsed .env map — used to resolve secret values and
// env var references (entries without =).
func Plan(cfg *Config, env map[string]string) ([]Step, error) {
	if errs := Validate(cfg); len(errs) > 0 {
		return nil, errs[0]
	}

	var steps []Step

	// ── 1. Compute ─────────────────────────────────────────────────────────
	// First server is master, rest are workers. Deterministic order.
	serverNames := sortedKeys(cfg.Servers)
	for i, name := range serverNames {
		srv := cfg.Servers[name]
		params := map[string]any{
			"type":   srv.Type,
			"region": srv.Region,
		}
		if i > 0 {
			params["worker"] = true
		}
		steps = append(steps, Step{Kind: StepComputeSet, Name: name, Params: params})
	}

	// ── 2. Volumes ─────────────────────────────────────────────────────────
	for _, name := range sortedKeys(cfg.Volumes) {
		vol := cfg.Volumes[name]
		steps = append(steps, Step{Kind: StepVolumeSet, Name: name, Params: map[string]any{
			"size":   vol.Size,
			"server": vol.Server,
		}})
	}

	// ── 3. Build ───────────────────────────────────────────────────────────
	for _, name := range sortedKeys(cfg.Build) {
		b := cfg.Build[name]
		steps = append(steps, Step{Kind: StepBuild, Name: name, Params: map[string]any{
			"source": b.Source,
		}})
	}

	// ── 4. Secrets ─────────────────────────────────────────────────────────
	// Collect all secret keys referenced by any service. Dedupe + sort.
	secretKeys := collectSecrets(cfg)
	for _, key := range secretKeys {
		val, ok := env[key]
		if !ok {
			return nil, fmt.Errorf("secret %q referenced by service but not found in env", key)
		}
		steps = append(steps, Step{Kind: StepSecretSet, Name: key, Params: map[string]any{
			"value": val,
		}})
	}

	// ── 5. Storage ─────────────────────────────────────────────────────────
	for _, name := range sortedKeys(cfg.Storage) {
		st := cfg.Storage[name]
		params := map[string]any{}
		if st.CORS {
			params["cors"] = true
		}
		if st.ExpireDays > 0 {
			params["expire_days"] = st.ExpireDays
		}
		if st.Bucket != "" {
			params["bucket"] = st.Bucket
		}
		steps = append(steps, Step{Kind: StepStorageSet, Name: name, Params: params})
	}

	// ── 6. Services ────────────────────────────────────────────────────────
	for _, name := range sortedKeys(cfg.Services) {
		svc := cfg.Services[name]
		params := map[string]any{}

		if svc.Image != "" {
			params["image"] = svc.Image
		}
		if svc.Build != "" {
			params["build"] = svc.Build
		}
		if svc.Port > 0 {
			params["port"] = svc.Port
		}
		if svc.Replicas > 0 {
			params["replicas"] = svc.Replicas
		}
		if svc.Command != "" {
			params["command"] = svc.Command
		}
		if svc.Health != "" {
			params["health"] = svc.Health
		}
		if svc.Server != "" {
			params["server"] = svc.Server
		}
		if len(svc.Volumes) > 0 {
			params["volumes"] = svc.Volumes
		}
		if len(svc.Secrets) > 0 {
			params["secrets"] = svc.Secrets
		}
		if len(svc.Storage) > 0 {
			params["storage"] = svc.Storage
		}

		// Resolve env: KEY=VALUE stays literal, bare KEY resolves from .env.
		var resolvedEnv []string
		for _, entry := range svc.Env {
			if strings.Contains(entry, "=") {
				resolvedEnv = append(resolvedEnv, entry)
			} else {
				val, ok := env[entry]
				if !ok {
					return nil, fmt.Errorf("services.%s.env: %q not found in env", name, entry)
				}
				resolvedEnv = append(resolvedEnv, entry+"="+val)
			}
		}
		if len(resolvedEnv) > 0 {
			params["env"] = resolvedEnv
		}

		steps = append(steps, Step{Kind: StepServiceSet, Name: name, Params: params})
	}

	// ── 7. DNS ─────────────────────────────────────────────────────────────
	for _, svcName := range sortedKeys(cfg.Domains) {
		domains := cfg.Domains[svcName]
		steps = append(steps, Step{Kind: StepDNSSet, Name: svcName, Params: map[string]any{
			"domains": []string(domains),
		}})
	}

	return steps, nil
}

// collectSecrets deduplicates and sorts all secret key references across services.
func collectSecrets(cfg *Config) []string {
	seen := map[string]bool{}
	for _, svc := range cfg.Services {
		for _, key := range svc.Secrets {
			seen[key] = true
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
